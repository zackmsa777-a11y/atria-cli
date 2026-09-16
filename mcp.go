package main

// mcp.go — Model Context Protocol client.
//
// MCP servers are declared in ~/.atria/config.json under "mcp_servers" or in a
// project-local .atria/mcp.json. On startup each server is spawned (stdio
// transport), initialized, and its tools are listed. Tools are surfaced to the
// model under the name mcp__<server>__<tool>, mirroring Claude Code's
// convention, and their results are returned to the loop exactly like a
// built-in tool result.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPServerConfig is one declared server. Command + Args run as a subprocess
// talking JSON-RPC over stdio.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

// MCPManager owns the live connections and the merged tool list.
type MCPManager struct {
	mu      sync.RWMutex
	clients map[string]*client.Client // server name -> live client
	tools   map[string]*mcp.Tool      // "mcp__name__tool" -> tool def
	servers map[string]*MCPServerConfig
}

func NewMCPManager() *MCPManager {
	return &MCPManager{
		clients: map[string]*client.Client{},
		tools:   map[string]*mcp.Tool{},
		servers: map[string]*MCPServerConfig{},
	}
}

// Load reads global config then the project-local .atria/mcp.json, which wins.
func (m *MCPManager) Load(cfg *Config) error {
	// global, from config.json
	if len(cfg.MCPServers) > 0 {
		for name, s := range cfg.MCPServers {
			if s.Enabled != nil && !*s.Enabled {
				continue
			}
			m.servers[name] = &s
		}
	}
	// project-local override file
	local := joinPath(cfg.CWD, ".atria", "mcp.json")
	if b, err := os.ReadFile(local); err == nil {
		var localServers map[string]MCPServerConfig
		if json.Unmarshal(b, &localServers) == nil {
			for name, s := range localServers {
				if s.Enabled != nil && !*s.Enabled {
					delete(m.servers, name) // local can disable a global
					continue
				}
				ss := s
				m.servers[name] = &ss
			}
		}
	}
	return nil
}

// Connect spawns and initializes every declared server, then lists tools.
// Failures are collected, not fatal: one broken server must not kill the agent.
func (m *MCPManager) Connect(ctx context.Context) []string {
	var problems []string
	for name, s := range m.servers {
		if _, err := exec.LookPath(s.Command); err != nil {
			problems = append(problems, fmt.Sprintf("mcp %s: %q not on PATH", name, s.Command))
			continue
		}
		env := os.Environ()
		for k, v := range s.Env {
			env = append(env, k+"="+v)
		}
		c, err := client.NewStdioMCPClient(s.Command, env, s.Args...)
		if err != nil {
			problems = append(problems, fmt.Sprintf("mcp %s: spawn: %v", name, err))
			continue
		}

		icx, cancel := context.WithTimeout(ctx, 30*time.Second)
		initReq := mcp.InitializeRequest{}
		initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
		initReq.Params.ClientInfo = mcp.Implementation{Name: "atria", Version: atriaVersion}
		if _, err := c.Initialize(icx, initReq); err != nil {
			cancel()
			_ = c.Close()
			problems = append(problems, fmt.Sprintf("mcp %s: init: %v", name, err))
			continue
		}
		cancel()

		tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
		if err != nil {
			_ = c.Close()
			problems = append(problems, fmt.Sprintf("mcp %s: list tools: %v", name, err))
			continue
		}

		m.mu.Lock()
		m.clients[name] = c
		for i := range tools.Tools {
			t := tools.Tools[i]
			m.tools[mcpToolName(name, t.Name)] = &t
		}
		m.mu.Unlock()
	}
	return problems
}

// Close shuts every server down.
func (m *MCPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.clients {
		_ = c.Close()
	}
	m.clients = map[string]*client.Client{}
	m.tools = map[string]*mcp.Tool{}
}

// Schemas returns tool definitions in the shape the agent loop sends to the
// model. MCP input schemas are passed through verbatim.
func (m *MCPManager) Schemas() []ToolSchema {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ToolSchema, 0, len(m.tools))
	for full, t := range m.tools {
		// ToolInputSchema is a struct; marshal it to JSON for our wire type.
		params, err := json.Marshal(t.InputSchema)
		if err != nil || len(params) == 0 || string(params) == "null" {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		} else {
			params = json.RawMessage(params)
		}
		desc := t.Description
		if desc == "" {
			desc = full
		}
		out = append(out, ToolSchema{
			Type: "function",
			Function: ToolFunc{
				Name:        full,
				Description: desc,
				Parameters:  params,
			},
		})
	}
	return out
}

// Has reports whether a tool name belongs to an MCP server.
func (m *MCPManager) Has(tool string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.tools[tool]
	return ok
}

// Call dispatches a tool call to its server and renders the result content as
// plain text for the model.
func (m *MCPManager) Call(ctx context.Context, fullTool string, args map[string]any) (string, error) {
	m.mu.RLock()
	t, ok := m.tools[fullTool]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("unknown mcp tool %q", fullTool)
	}
	server, short, err := splitMCPTool(fullTool)
	if err != nil {
		m.mu.RUnlock()
		return "", err
	}
	c, ok := m.clients[server]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("mcp server %q not connected", server)
	}
	m.mu.RUnlock()

	req := mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      short,
		Arguments: args,
	}}
	// Validate against the server's schema first so a bad call surfaces as a
	// model-visible tool error instead of a protocol error.
	if err := validateMCPArgs(t, args); err != nil {
		return "", err
	}

	res, err := c.CallTool(ctx, req)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", fmt.Errorf("%s", joinMCPContent(res.Content))
	}
	return joinMCPContent(res.Content), nil
}

// --- naming ---

// mcpToolName builds "mcp__<server>__<tool>", the same convention Claude Code
// uses for --allowedTools. The prefix keeps MCP tools out of the built-in
// namespace and makes the source server obvious in the transcript.
func mcpToolName(server, tool string) string {
	return "mcp__" + server + "__" + tool
}

func splitMCPTool(full string) (server, tool string, err error) {
	parts := strings.SplitN(full, "__", 3)
	if len(parts) != 3 || parts[0] != "mcp" {
		return "", "", fmt.Errorf("malformed mcp tool name %q", full)
	}
	return parts[1], parts[2], nil
}

// joinMCPContent flattens a CallToolResult into text. TextContent is passed
// through; anything else (image, audio, embedded resource) is summarized so
// the model knows it existed even though a text model cannot see it.
func joinMCPContent(contents []mcp.Content) string {
	var b strings.Builder
	for _, c := range contents {
		switch v := c.(type) {
		case mcp.TextContent:
			b.WriteString(v.Text)
			b.WriteString("\n")
		default:
			fmt.Fprintf(&b, "[unsupported content type: %T]\n", c)
		}
	}
	return strings.TrimSpace(b.String())
}

// validateMCPArgs rejects missing required properties before a round trip.
func validateMCPArgs(t *mcp.Tool, args map[string]any) error {
	raw, err := json.Marshal(t.InputSchema)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	for _, k := range schema.Required {
		if _, ok := args[k]; !ok {
			return fmt.Errorf("missing required argument %q", k)
		}
	}
	return nil
}
