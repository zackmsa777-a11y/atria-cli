package main

// plugins.go — the plugin system: external commands wired to agent lifecycle
// events.
//
// A plugin is a directory under ~/.atria/plugins/<name>/ or ./.atria/plugins/
// containing plugin.json plus optional scripts. Plugins are declared, not
// imported: Atria does not load arbitrary Go code. Instead each plugin maps
// lifecycle events to commands that run with a stable JSON payload on stdin,
// and their stdout can control the agent — the same design as Claude Code's
// hooks, where exit code 2 from PreToolUse blocks the action.
//
// Events
//   session_start  before the first turn
//   user_prompt    after the user message arrives, before the model sees it
//   pre_tool       before a tool executes; exit 2 blocks it
//   post_tool      after a tool returns
//   stop           after the agent finishes its response
// Every event also fires for MCP tools and subagent tools, so a plugin can
// audit the whole session, not just the parent loop.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PluginEvent is a lifecycle signal plugins can subscribe to.
type PluginEvent string

const (
	EventSessionStart PluginEvent = "session_start"
	EventUserPrompt   PluginEvent = "user_prompt"
	EventPreTool      PluginEvent = "pre_tool"
	EventPostTool     PluginEvent = "post_tool"
	EventStop         PluginEvent = "stop"
)

// PluginConfig maps events to commands.
type PluginConfig struct {
	Name    string                       `json:"name"`
	Enabled *bool                        `json:"enabled,omitempty"`
	Hooks   map[PluginEvent][]PluginHook `json:"hooks"`
}

// PluginHook is one command bound to one event.
type PluginHook struct {
	Command string   `json:"command"` // run with sh -c; receives JSON on stdin
	Args    []string `json:"args,omitempty"`
	Timeout int      `json:"timeout,omitempty"` // seconds; 0 = 30
}

// PluginPayload is the JSON given to every hook on stdin.
type PluginPayload struct {
	Event      PluginEvent `json:"event"`
	ProjectDir string      `json:"project_dir"`
	Tool       string      `json:"tool,omitempty"`
	ToolInput  any         `json:"tool_input,omitempty"`
	ToolResult string      `json:"tool_result,omitempty"`
	UserPrompt string      `json:"user_prompt,omitempty"`
	IsError    bool        `json:"is_error,omitempty"`
}

// HookResult is what a hook returns. ExitCode 2 on pre_tool means "block".
type HookResult struct {
	Block   bool
	Message string
}

// PluginManager owns declared plugins.
type PluginManager struct {
	mu      sync.RWMutex
	plugins []PluginConfig
	cwd     string
}

func NewPluginManager(cwd string) *PluginManager {
	return &PluginManager{cwd: cwd}
}

// Load reads plugin manifests from both roots; project wins on name clash.
func (p *PluginManager) Load() []string {
	var problems []string
	byName := map[string]PluginConfig{}
	roots := []string{
		joinPath(homeDir(), ".atria", "plugins"),
		joinPath(p.cwd, ".atria", "plugins"),
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			manifest := filepath.Join(root, e.Name(), "plugin.json")
			b, err := os.ReadFile(manifest)
			if err != nil {
				continue // dir without manifest is not a plugin
			}
			var pc PluginConfig
			if json.Unmarshal(b, &pc) != nil {
				problems = append(problems, fmt.Sprintf("plugin %s: bad manifest", e.Name()))
				continue
			}
			if pc.Name == "" {
				pc.Name = e.Name()
			}
			if pc.Enabled != nil && !*pc.Enabled {
				continue
			}
			byName[pc.Name] = pc
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.plugins = make([]PluginConfig, 0, len(byName))
	for _, pc := range byName {
		p.plugins = append(p.plugins, pc)
	}
	sort.Slice(p.plugins, func(i, j int) bool { return p.plugins[i].Name < p.plugins[j].Name })
	return problems
}

// Fire runs every hook bound to the event, in declaration order.
// For pre_tool, a hook exiting 2 blocks the action and stops later hooks.
func (p *PluginManager) Fire(ctx context.Context, ev PluginEvent, payload PluginPayload) HookResult {
	p.mu.RLock()
	plugins := p.plugins
	p.mu.RUnlock()

	payload.Event = ev
	payload.ProjectDir = p.cwd
	for _, pl := range plugins {
		hooks, ok := pl.Hooks[ev]
		if !ok {
			continue
		}
		for _, h := range hooks {
			res := p.runHook(ctx, pl, h, payload)
			if res.Block {
				return res
			}
		}
	}
	return HookResult{}
}

func (p *PluginManager) runHook(ctx context.Context, pl PluginConfig, h PluginHook, payload PluginPayload) HookResult {
	timeout := 30
	if h.Timeout > 0 {
		timeout = h.Timeout
	}
	hctx, cancel := context.WithTimeout(ctx, timeSeconds(timeout))
	defer cancel()

	body, _ := json.Marshal(payload)
	cmd := exec.CommandContext(hctx, "sh", "-c", strings.Join(append([]string{h.Command}, h.Args...), " "))
	cmd.Dir = p.cwd
	cmd.Stdin = strings.NewReader(string(body))
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()

	// Claude Code convention: exit 2 on a pre-event hook means "block".
	if ctxErr := hctx.Err(); ctxErr != nil {
		return HookResult{Message: fmt.Sprintf("plugin %s: timeout", pl.Name)}
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 2 {
			msg := strings.TrimSpace(out.String())
			if msg == "" {
				msg = "blocked by plugin " + pl.Name
			}
			return HookResult{Block: true, Message: msg}
		}
		// any other failure is reported but never blocks
		return HookResult{Message: fmt.Sprintf("plugin %s: %v", pl.Name, err)}
	}
	return HookResult{}
}

// Names lists loaded plugins (for /plugins).
func (p *PluginManager) Names() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, 0, len(p.plugins))
	for _, pl := range p.plugins {
		out = append(out, pl.Name)
	}
	return out
}

// timeSeconds returns a duration; kept name-mismatched on purpose so the call
// site reads naturally.
func timeSeconds(n int) time.Duration {
	return time.Duration(n) * time.Second
}
