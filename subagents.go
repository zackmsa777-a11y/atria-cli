package main

// subagents.go — delegated agents.
//
// A subagent is a full agent loop with its own conversation history running
// inside the parent process. It inherits the parent config (model, approval
// policy, security mode) but gets a scoped system prompt and a restricted tool
// set, so a security-review agent can be handed Read-only tools while a
// refactor agent keeps the full set.
//
// Subagents are defined in .atria/agents/<name>.md or ~/.atria/agents/, with
// frontmatter mirroring Claude Code's agent format: name, description, model,
// tools. Invoked through the agent tool, which Claude Code ships as AgentInput:
// description, prompt, subagent_type, model override, and run_in_background.
//
// Background vs foreground follows Claude Code's rule: a subagent runs in the
// background unless the parent's very next action depends on its result. The
// parent is notified when a background subagent completes.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SubagentDef is a declared agent type.
type SubagentDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Model       string   `json:"model,omitempty"` // override; empty = inherit parent
	Tools       []string `json:"tools,omitempty"` // empty = inherit full set
	System      string   `json:"system,omitempty"`
}

// SubagentManager holds declared agent types and running instances.
type SubagentManager struct {
	mu      sync.RWMutex
	defs    map[string]*SubagentDef
	running map[string]*runningSub // live instances, by name
	parent  *Agent
	mcp     *MCPManager
	skills  *SkillManager
	plugins *PluginManager
}

type runningSub struct {
	def     *SubagentDef
	result  string
	err     error
	done    chan struct{}
	started time.Time
}

func NewSubagentManager(parent *Agent, mcp *MCPManager, skills *SkillManager, plugins *PluginManager) *SubagentManager {
	return &SubagentManager{
		defs:    map[string]*SubagentDef{},
		running: map[string]*runningSub{},
		parent:  parent,
		mcp:     mcp,
		skills:  skills,
		plugins: plugins,
	}
}

// Load reads agent definitions from both roots; project wins.
func (s *SubagentManager) Load(cfg *Config) []string {
	var problems []string
	roots := []string{
		joinPath(homeDir(), ".atria", "agents"),
		joinPath(cfg.CWD, ".atria", "agents"),
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(root, e.Name()))
			if err != nil {
				continue
			}
			def, err := parseAgentMarkdown(string(b))
			if err != nil {
				problems = append(problems, fmt.Sprintf("agent %s: %v", e.Name(), err))
				continue
			}
			if def.Name == "" {
				def.Name = strings.TrimSuffix(e.Name(), ".md")
			}
			s.mu.Lock()
			s.defs[def.Name] = def
			s.mu.Unlock()
		}
	}
	return problems
}

// Names lists declared agent types (for /agents).
func (s *SubagentManager) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.defs))
	for n := range s.defs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Run launches a subagent. The returned channel closes when the agent finishes
// (foreground callers read result immediately; background callers are notified).
func (s *SubagentManager) Run(ctx context.Context, name, prompt, modelOverride string, background bool) (*runningSub, error) {
	s.mu.RLock()
	def, ok := s.defs[name]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("unknown subagent %q; declared: %s", name, strings.Join(s.Names(), ", "))
	}

	sub := &runningSub{def: def, done: make(chan struct{}), started: time.Now()}
	s.mu.Lock()
	s.running[name] = sub
	s.mu.Unlock()

	run := func() {
		defer close(sub.done)
		// A subagent is a scoped clone of the parent loop: same transport and
		// approval policy, its own history, a filtered tool set.
		child := s.parent.scopeFor(def, modelOverride)
		ch := make(chan AgentEvent, 64)
		go child.Run(ctx, prompt, ch)
		var last string
		for ev := range ch {
			if ev.Kind == "text" || ev.Kind == "delta" {
				last = ev.Text
			}
			if ev.Kind == "error" {
				sub.err = fmt.Errorf("%s", ev.Text)
			}
			// surface subagent progress into the parent transcript
			s.plugins.Fire(ctx, EventPostTool, PluginPayload{
				Tool:       "agent:" + name,
				ToolResult: ev.Text,
				IsError:    ev.Kind == "error",
			})
		}
		sub.result = last
	}

	if background {
		go run()
		return sub, nil
	}
	run()
	return sub, nil
}

// Wait blocks until a background subagent finishes.
func (s *SubagentManager) Wait(name string) (string, error) {
	s.mu.RLock()
	sub, ok := s.running[name]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no running subagent named %q", name)
	}
	<-sub.done
	return sub.result, sub.err
}

// ListRunning reports subagents that have not finished.
func (s *SubagentManager) ListRunning() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []string
	for name, sub := range s.running {
		select {
		case <-sub.done:
		default:
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// --- parsing ---

func parseAgentMarkdown(raw string) (*SubagentDef, error) {
	def := &SubagentDef{}
	if !strings.HasPrefix(raw, "---") {
		def.System = raw
		return def, nil
	}
	rest := strings.TrimPrefix(raw, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, fmt.Errorf("unterminated frontmatter")
	}
	header := rest[:end]
	def.System = strings.TrimLeft(rest[end+4:], "\n")
	for _, line := range strings.Split(header, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), "\"")
		switch k {
		case "name":
			def.Name = v
		case "description":
			def.Description = v
		case "model":
			def.Model = v
		case "tools":
			for _, t := range strings.Split(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					def.Tools = append(def.Tools, t)
				}
			}
		}
	}
	// YAML flow sequence: tools: [Read, Bash]
	if rawTools := extractFlowList(header, "tools"); len(rawTools) > 0 {
		def.Tools = rawTools
	}
	return def, nil
}

// extractFlowList parses a single "key: [a, b]" flow sequence if present.
func extractFlowList(header, key string) []string {
	for _, line := range strings.Split(header, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		v = strings.TrimSpace(v)
		if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
			continue
		}
		inner := strings.Trim(v, "[]")
		var out []string
		for _, item := range strings.Split(inner, ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, strings.Trim(item, "\""))
			}
		}
		return out
	}
	return nil
}

var _ = json.Marshal
