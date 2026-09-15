package main

// agent.go — the plan → act → observe loop that talks to the OpenAI-compatible
// endpoint with SSE streaming. Runs entirely in a goroutine; the TUI never blocks on it.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Message is OpenAI chat-format. Tool calls/results are stored verbatim so
// history replays correctly on resume.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
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

// ApprovalDecision models user choice on interactive action confirmation.
type ApprovalDecision int

const (
	DecisionDeny ApprovalDecision = iota
	DecisionAllowOnce
	DecisionAllowAlways
)

type ApprovalRequest struct {
	Tool   string
	Detail string
	Resp   chan ApprovalDecision
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
	Kind        string // delta | think_delta | think | tool | text | done | error | ask_approval
	Text        string
	Tool        string
	Detail      string
	Usage       Usage
	ApprovalReq *ApprovalRequest
}

type partialToolCall struct {
	id   string
	name string
	args strings.Builder
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
	b.WriteString("You are Atria, an agentic coding and security-research CLI powered by the Atria ASI foundation model. ")
	b.WriteString("Work in the workspace the user gives you. Prefer reading files or using replace_file_content for surgical edits. ")
	b.WriteString("When you make a change, verify it (run the code, read the file back). ")
	b.WriteString("Never fabricate results: if a tool fails, report the failure plainly.\n")
	if a.cfg.Security {
		b.WriteString("\nSecurity mode is ON. All recon actions are written to .atria/audit.log ")
		b.WriteString("with the recorded authorization scope: " + a.cfg.HackScope + "\n")
	}
	return b.String()
}

// Run drives the whole loop to completion, streaming events out on ch.
// ctx is cancelled on Ctrl-C / Esc; the loop checks it between tool calls.
func (a *Agent) Run(ctx context.Context, prompt string, ch chan<- AgentEvent) {
	a.messages = append(a.messages, Message{Role: "user", Content: prompt})

	for a.iteration = 1; a.iteration <= a.cfg.MaxIter; a.iteration++ {
		select {
		case <-ctx.Done():
			ch <- AgentEvent{Kind: "error", Text: "cancelled", Usage: a.usage}
			return
		default:
		}

		asst, toolCalls, err := a.chatStream(ctx, ch)
		if err != nil {
			if ctx.Err() != nil {
				ch <- AgentEvent{Kind: "error", Text: "cancelled", Usage: a.usage}
				return
			}
			ch <- AgentEvent{Kind: "error", Text: err.Error(), Usage: a.usage}
			return
		}
		a.messages = append(a.messages, asst)

		if len(toolCalls) == 0 {
			// `done` is a marker only, never a second copy of the answer.
			ch <- AgentEvent{Kind: "done", Text: "", Usage: a.usage}
			return
		}

		// execute each tool call, append results
		for _, call := range toolCalls {
			ch <- AgentEvent{
				Kind:   "tool",
				Tool:   call.Function.Name,
				Detail: call.Function.Arguments,
				Usage:  a.usage,
			}
			out := a.execute(ctx, call, ch)
			a.messages = append(a.messages, Message{
				Role:       "tool",
				Content:    out,
				ToolCallID: call.ID,
			})
		}
	}
	ch <- AgentEvent{Kind: "error", Text: fmt.Sprintf("iteration cap (%d) reached", a.cfg.MaxIter), Usage: a.usage}
}

// chatStream opens an SSE stream to the model and sends live deltas to ch.
func (a *Agent) chatStream(ctx context.Context, ch chan<- AgentEvent) (Message, []ToolCall, error) {
	req := map[string]any{
		"model":       a.cfg.Model,
		"messages":    a.messages,
		"tools":       a.tools,
		"temperature": 0.4,
		"stream":      true,
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

	reader := bufio.NewReader(resp.Body)
	var fullContent strings.Builder
	var fullReasoning strings.Builder
	toolCallMap := make(map[int]*partialToolCall)

	type streamChunk struct {
		Choices []struct {
			Index int `json:"index"`
			Delta struct {
				Role             string `json:"role"`
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
		Usage *Usage `json:"usage"`
	}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			if ctx.Err() != nil {
				return Message{}, nil, ctx.Err()
			}
			break
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			a.usage.PromptTokens += chunk.Usage.PromptTokens
			a.usage.CompletionTokens += chunk.Usage.CompletionTokens
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		if choice.Delta.Content != "" {
			fullContent.WriteString(choice.Delta.Content)
			ch <- AgentEvent{Kind: "delta", Text: choice.Delta.Content, Usage: a.usage}
		}

		rText := choice.Delta.ReasoningContent
		if rText == "" {
			rText = choice.Delta.Reasoning
		}
		if rText != "" {
			fullReasoning.WriteString(rText)
			ch <- AgentEvent{Kind: "think_delta", Text: rText, Usage: a.usage}
		}

		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			pt, exists := toolCallMap[idx]
			if !exists {
				pt = &partialToolCall{id: tc.ID, name: tc.Function.Name}
				toolCallMap[idx] = pt
			}
			if tc.ID != "" && pt.id == "" {
				pt.id = tc.ID
			}
			if tc.Function.Name != "" && pt.name == "" {
				pt.name = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				pt.args.WriteString(tc.Function.Arguments)
			}
		}
	}

	var toolCalls []ToolCall
	for idx := 0; idx < len(toolCallMap); idx++ {
		if pt, ok := toolCallMap[idx]; ok {
			toolCalls = append(toolCalls, ToolCall{
				ID:   pt.id,
				Type: "function",
				Function: struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				}{
					Name:      pt.name,
					Arguments: pt.args.String(),
				},
			})
		}
	}

	msg := Message{
		Role:      "assistant",
		Content:   fullContent.String(),
		ToolCalls: toolCalls,
	}
	return msg, toolCalls, nil
}

// execute runs one tool call, prompting for approval if in 'ask' mode.
func (a *Agent) execute(ctx context.Context, call ToolCall, ch chan<- AgentEvent) string {
	name := call.Function.Name
	var args map[string]any
	_ = json.Unmarshal([]byte(call.Function.Arguments), &args)

	str := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}
	num := func(k string) int {
		if v, ok := args[k].(float64); ok {
			return int(v)
		}
		return 0
	}

	if a.cfg.Approval == "ask" && isActionTool(name) {
		req := &ApprovalRequest{
			Tool:   name,
			Detail: summarizeToolCall(name, str, args),
			Resp:   make(chan ApprovalDecision, 1),
		}
		ch <- AgentEvent{
			Kind:        "ask_approval",
			Tool:        name,
			Detail:      req.Detail,
			ApprovalReq: req,
			Usage:       a.usage,
		}

		select {
		case <-ctx.Done():
			return "[cancelled by user]"
		case dec := <-req.Resp:
			if dec == DecisionDeny {
				return "[action denied by user]"
			}
			if dec == DecisionAllowAlways {
				a.cfg.Approval = "full-auto"
			}
		}
	}

	resCh := make(chan string, 1)
	go func() { resCh <- dispatchTool(ctx, a.cfg.CWD, name, str, num, args) }()

	select {
	case <-ctx.Done():
		return "[cancelled by user]"
	case out := <-resCh:
		if a.cfg.Security {
			a.audit(name, call.Function.Arguments)
		}
		return truncate(out, 20000)
	}
}

func isActionTool(name string) bool {
	return name == "bash" || name == "write_file" || name == "replace_file_content"
}

func summarizeToolCall(name string, str func(string) string, args map[string]any) string {
	switch name {
	case "bash":
		return str("command")
	case "write_file":
		return fmt.Sprintf("write %s (%d bytes)", str("path"), len(str("content")))
	case "replace_file_content":
		return fmt.Sprintf("patch %s", str("path"))
	default:
		return name
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

// dispatchTool routes execution to the specified tool.
func dispatchTool(ctx context.Context, cwd, name string, str func(string) string, num func(string) int, args map[string]any) string {
	switch name {
	case "bash":
		return runBash(ctx, cwd, str("command"))
	case "view_file":
		return readFile(cwd, str("path"), num("start_line"), num("end_line"))
	case "write_file":
		return writeFile(cwd, str("path"), str("content"))
	case "replace_file_content":
		return replaceFileContent(cwd, str("path"), str("target_content"), str("replacement_content"))
	case "list_dir":
		return listDir(cwd, str("path"))
	case "grep":
		return grepFiles(cwd, str("pattern"), str("path"))
	case "web_fetch":
		return webFetch(ctx, str("url"))
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

// --- in-process tools ---

func resolvePath(cwd, p string) string {
	p = expandPath(p)
	if filepath.IsAbs(p) || cwd == "" {
		return p
	}
	return filepath.Join(expandPath(cwd), p)
}

func runBash(ctx context.Context, cwd, cmd string) string {
	cmdCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	c := exec.CommandContext(cmdCtx, "bash", "-c", cmd)
	if cwd != "" {
		c.Dir = expandPath(cwd)
	}
	out, err := c.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("error: %v\n%s", err, truncate(string(out), 4000))
	}
	return truncate(string(out), 8000)
}

func readFile(cwd, p string, startLine, endLine int) string {
	if p == "" {
		p = "."
	}
	b, err := os.ReadFile(resolvePath(cwd, p))
	if err != nil {
		return "error: " + err.Error()
	}
	lines := strings.Split(string(b), "\n")
	total := len(lines)
	if startLine <= 0 && endLine <= 0 {
		return truncate(string(b), 20000)
	}
	if startLine <= 0 {
		startLine = 1
	}
	if endLine <= 0 || endLine > total {
		endLine = total
	}
	if startLine > total {
		return fmt.Sprintf("error: start_line (%d) exceeds total lines (%d)", startLine, total)
	}
	if startLine > endLine {
		return fmt.Sprintf("error: start_line (%d) > end_line (%d)", startLine, endLine)
	}

	var sb strings.Builder
	for i := startLine; i <= endLine; i++ {
		fmt.Fprintf(&sb, "%4d | %s\n", i, lines[i-1])
	}
	return truncate(sb.String(), 20000)
}

func writeFile(cwd, p, content string) string {
	full := resolvePath(cwd, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "error: " + err.Error()
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("wrote %s (%d bytes)", p, len(content))
}

func replaceFileContent(cwd, p, target, replacement string) string {
	if p == "" || target == "" {
		return "error: path and target_content are required"
	}
	full := resolvePath(cwd, p)
	b, err := os.ReadFile(full)
	if err != nil {
		return "error: " + err.Error()
	}
	content := string(b)
	count := strings.Count(content, target)
	if count == 0 {
		return fmt.Sprintf("error: target_content not found in %s", p)
	}
	if count > 1 {
		return fmt.Sprintf("error: target_content found %d times in %s; include more context lines to ensure uniqueness", count, p)
	}

	newContent := strings.Replace(content, target, replacement, 1)
	if err := os.WriteFile(full, []byte(newContent), 0o644); err != nil {
		return "error: " + err.Error()
	}

	targetLines := strings.Count(target, "\n") + 1
	replLines := strings.Count(replacement, "\n") + 1
	return fmt.Sprintf("replaced in %s (-%d lines, +%d lines)", p, targetLines, replLines)
}

func listDir(cwd, p string) string {
	if p == "" {
		p = "."
	}
	entries, err := os.ReadDir(resolvePath(cwd, p))
	if err != nil {
		return "error: " + err.Error()
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() {
			fmt.Fprintf(&b, "%s/\n", e.Name())
		} else {
			info, _ := e.Info()
			if info != nil {
				fmt.Fprintf(&b, "%s  (%d)\n", e.Name(), info.Size())
			} else {
				fmt.Fprintf(&b, "%s\n", e.Name())
			}
		}
	}
	return b.String()
}

func grepFiles(cwd, pattern, root string) string {
	if root == "" {
		root = "."
	}
	fullRoot := resolvePath(cwd, root)
	var b strings.Builder
	_ = filepath.WalkDir(fullRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != fullRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
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
				rel, relErr := filepath.Rel(fullRoot, path)
				if relErr != nil {
					rel = path
				}
				fmt.Fprintf(&b, "%s:%d: %s\n", rel, lineNo, truncate(scanner.Text(), 200))
			}
		}
		return nil
	})
	return b.String()
}

func webFetch(ctx context.Context, u string) string {
	if !strings.HasPrefix(u, "http") {
		return "error: url must start with http"
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "error: " + err.Error()
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "error: " + err.Error()
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 1024*1024)
	b, _ := io.ReadAll(limited)
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
