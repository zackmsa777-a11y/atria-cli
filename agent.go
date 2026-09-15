package main

// agent.go — the plan → act → observe loop that talks to the OpenAI-compatible
// endpoint. Runs entirely in a goroutine; the TUI never blocks on it.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"io/fs"
	"strings"
	"time"
)

// Message is OpenAI chat-format. Tool calls/results are stored verbatim so
// history replays correctly on resume.
type Message struct {
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	ToolCalls  []ToolCall      `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolSchema struct {
	Type     string   `json:"type"`
	Function ToolFunc `json:"function"`
}

type ToolFunc struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// Agent holds the loop state.
type Agent struct {
	cfg       *Config
	client    *http.Client
	messages  []Message
	tools     []ToolSchema
	iteration int
	usage     Usage
	aborted   bool
	auditLog  string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// AgentEvent is pushed to the TUI as the loop progresses.
type AgentEvent struct {
	Kind   string // think | tool | text | done | error
	Text   string
	Tool   string
	Detail string
}

func NewAgent(cfg *Config) *Agent {
	a := &Agent{
		cfg:    cfg,
		client: &http.Client{Timeout: 300 * time.Second},
	}
	a.messages = []Message{{Role: "system", Content: a.systemPrompt()}}
	a.tools = a.toolset()
	a.auditLog = filepath.Join(cfg.CWD, ".atria", "audit.log")
	return a
}

func (a *Agent) systemPrompt() string {
	var b strings.Builder
	b.WriteString("You are Atria, an agentic coding and security-research CLI. ")
	b.WriteString("Work in the workspace the user gives you. Prefer reading files before editing. ")
	b.WriteString("When you make a change, verify it (run the code, read the file back). ")
	b.WriteString("Never fabricate results: if a tool fails, report the failure plainly.\n")
	if a.cfg.Security {
		b.WriteString("\nSecurity mode is ON. All recon actions are written to .atria/audit.log ")
		b.WriteString("with the recorded authorization scope: " + a.cfg.HackScope + "\n")
	}
	return b.String()
}

// Run drives the whole loop to completion, streaming events out on ch.
// ctx is cancelled on Ctrl-C; the loop checks it between tool calls.
func (a *Agent) Run(ctx context.Context, prompt string, ch chan<- AgentEvent) {
	a.messages = append(a.messages, Message{Role: "user", Content: prompt})

	for a.iteration = 1; a.iteration <= a.cfg.MaxIter; a.iteration++ {
		select {
		case <-ctx.Done():
			ch <- AgentEvent{Kind: "error", Text: "cancelled"}
			return
		default:
		}

		asst, toolCalls, err := a.chat(ctx, ch)
		if err != nil {
			if ctx.Err() != nil {
				ch <- AgentEvent{Kind: "error", Text: "cancelled"}
				return
			}
			ch <- AgentEvent{Kind: "error", Text: err.Error()}
			return
		}
		a.messages = append(a.messages, asst)

		if len(toolCalls) == 0 {
			// `text` was already emitted in chat() for this content; `done` is a
			// marker only, never a second copy of the answer.
			ch <- AgentEvent{Kind: "done", Text: ""}
			return
		}

		// execute each tool call, append results
		for _, call := range toolCalls {
			out := a.execute(ctx, call)
			a.messages = append(a.messages, Message{
				Role:       "tool",
				Content:    out,
				ToolCallID: call.ID,
			})
		}
	}
	ch <- AgentEvent{Kind: "error", Text: fmt.Sprintf("iteration cap (%d) reached", a.cfg.MaxIter)}
}

// chat does one round-trip. Streaming deltas are forwarded as they arrive.
func (a *Agent) chat(ctx context.Context, ch chan<- AgentEvent) (Message, []ToolCall, error) {
	req := map[string]any{
		"model":       a.cfg.Model,
		"messages":    a.messages,
		"tools":       a.tools,
		"temperature": 0.4,
	}
	if a.cfg.MaxTokens > 0 {
		req["max_tokens"] = a.cfg.MaxTokens
	}
	if a.cfg.Reasoning != "" {
		req["reasoning_effort"] = a.cfg.Reasoning
	}

	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", a.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.cfg.APIKey)

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return Message{}, nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(resp.Body)
		return Message{}, nil, fmt.Errorf("endpoint %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}

	// parse the full JSON (we want tool_calls + usage); stream text as we go
	var parsed struct {
		Choices []struct {
			Message      Message `json:"message"`
			Delta        Message `json:"delta"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return Message{}, nil, err
	}
	if len(parsed.Choices) == 0 {
		return Message{}, nil, fmt.Errorf("endpoint returned no choices")
	}
	msg := parsed.Choices[0].Message
	a.usage.PromptTokens += parsed.Usage.PromptTokens
	a.usage.CompletionTokens += parsed.Usage.CompletionTokens

	if strings.TrimSpace(msg.Content) != "" {
		ch <- AgentEvent{Kind: "text", Text: msg.Content}
	}
	return msg, msg.ToolCalls, nil
}

// execute runs one tool call and returns the string result.
func (a *Agent) execute(ctx context.Context, call ToolCall) string {
	name := call.Function.Name
	var args map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)

	str := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}

	ch := make(chan string, 1)
	go func() { ch <- dispatchTool(name, str, args) }()

	select {
	case <-ctx.Done():
		return "[cancelled by user]"
	case out := <-ch:
		if a.cfg.Security {
			a.audit(name, call.Function.Arguments)
		}
		return truncate(out, 20000)
	}
}

func (a *Agent) audit(tool, args string) {
	_ = os.MkdirAll(filepath.Dir(a.auditLog), 0o755)
	f, err := os.OpenFile(a.auditLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s | %s | %s | %s\n", nowStamp(), a.cfg.HackScope, tool, args)
}

// dispatchTool is the pure tool router — in-process tools only for now.
func dispatchTool(name string, str func(string) string, args map[string]any) string {
	switch name {
	case "bash":
		return runBash(str("command"))
	case "view_file":
		return readFile(str("path"))
	case "write_file":
		return writeFile(str("path"), str("content"))
	case "list_dir":
		return listDir(str("path"))
	case "grep":
		return grepFiles(str("pattern"), str("path"))
	case "web_fetch":
		return webFetch(str("url"))
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

// --- in-process tools ---

func runBash(cmd string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "bash", "-c", cmd).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("error: %v\n%s", err, truncate(string(out), 4000))
	}
	return truncate(string(out), 8000)
}

func readFile(p string) string {
	if p == "" {
		p = "."
	}
	b, err := os.ReadFile(expandPath(p))
	if err != nil {
		return "error: " + err.Error()
	}
	return truncate(string(b), 20000)
}

func writeFile(p, content string) string {
	full := expandPath(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "error: " + err.Error()
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("wrote %s (%d bytes)", p, len(content))
}

func listDir(p string) string {
	if p == "" {
		p = "."
	}
	entries, err := os.ReadDir(expandPath(p))
	if err != nil {
		return "error: " + err.Error()
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
		} else {
			info, _ := e.Info()
			fmt.Fprintf(&b, "%s  (%d)\n", e.Name(), info.Size())
		}
	}
	return b.String()
}

func grepFiles(pattern, root string) string {
	if root == "" {
		root = "."
	}
	var b strings.Builder
	_ = filepath.WalkDir(expandPath(root), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			if strings.Contains(scanner.Text(), pattern) {
				fmt.Fprintf(&b, "%s:%d: %s\n", path, lineNo, truncate(scanner.Text(), 200))
			}
		}
		return nil
	})
	return b.String()
}

func webFetch(u string) string {
	if !strings.HasPrefix(u, "http") {
		return "error: url must start with http"
	}
	resp, err := http.Get(u)
	if err != nil {
		return "error: " + err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return truncate(string(b), 10000)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n…[truncated]"
}

var _ = io.EOF
