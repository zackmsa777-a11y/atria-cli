package main

// tools.go — tool definitions exposed to the model.

import "encoding/json"

func (a *Agent) toolset() []ToolSchema {
	return []ToolSchema{
		{Type: "function", Function: ToolFunc{
			Name:        "bash",
			Description: "Run a shell command in the workspace. Returns combined stdout+stderr.",
			Parameters:  raw(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "view_file",
			Description: "Read a file's contents.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "write_file",
			Description: "Write content to a file, replacing it entirely.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "list_dir",
			Description: "List directory entries with sizes.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "grep",
			Description: "Search file contents for a substring.",
			Parameters:  raw(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"}},"required":["pattern"]}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "web_fetch",
			Description: "Fetch a URL and return the raw response body.",
			Parameters:  raw(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
		}},
	}
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }
