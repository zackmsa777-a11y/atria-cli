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
		{Type: "function", Function: ToolFunc{
			Name:        "agent",
			Description: "Delegate a bounded task to a specialized subagent. The subagent runs its own agent loop with a scoped system prompt and a restricted tool set; it cannot see this conversation's history. Use for work that would flood context (broad codebase surveys, deep searches, parallel explorations) or that needs a specialist. Background by default: set run_in_background=false only when your next action depends on the result.",
			Parameters:  raw(`{"type":"object","properties":{"description":{"type":"string","description":"A short (3-5 word) description of the task"},"prompt":{"type":"string","description":"The task for the subagent to perform"},"subagent_type":{"type":"string","description":"Name of the declared agent type to use"},"model":{"type":"string","description":"Optional model override; empty inherits the parent"},"run_in_background":{"type":"boolean","description":"Run detached and get notified on completion (default true)"}},"required":["description","prompt","subagent_type"]}`),
		}},
		{Type: "function", Function: ToolFunc{
			Name:        "skill",
			Description: "Invoke a loaded skill by name to load its full procedure into context. Skills are markdown procedures; their descriptions are in the system prompt. Pass a relative path (e.g. references/api.md) to load a linked file from the skill directory instead.",
			Parameters:  raw(`{"type":"object","properties":{"name":{"type":"string","description":"Skill name"},"file":{"type":"string","description":"Optional relative path to a linked file inside the skill directory"}},"required":["name"]}`),
		}},
	}
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }
