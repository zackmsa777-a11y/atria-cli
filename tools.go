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
			Description: "Read a file's contents, optionally between start_line and end_line (1-indexed).",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"start_line":{"type":"integer"},"end_line":{"type":"integer"}},"required":["path"]}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "write_file",
			Description: "Write content to a file, replacing it entirely.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "replace_file_content",
			Description: "Replace an exact target block of text in a file with new content. Preserves the rest of the file.",
			Parameters:  raw(`{"type":"object","properties":{"path":{"type":"string"},"target_content":{"type":"string"},"replacement_content":{"type":"string"}},"required":["path","target_content","replacement_content"]}`),
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
