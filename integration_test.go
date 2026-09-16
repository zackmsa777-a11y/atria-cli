package main

// integration_test.go — end-to-end proof that the three extension subsystems
// work against real artifacts on disk and a real stdio MCP server subprocess.
// Excludes itself from the default short run via t.Skip unless ATRIA_E2E=1.

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestE2EExtensions(t *testing.T) {
	if os.Getenv("ATRIA_E2E") != "1" {
		t.Skip("set ATRIA_E2E=1 to run the end-to-end extension test")
	}
	serverBin := os.Getenv("SHOUT_SERVER")
	if serverBin == "" {
		t.Skip("set SHOUT_SERVER to the built shout-server binary")
	}

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".atria"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 1. skill on disk
	skDir := filepath.Join(dir, ".atria", "skills", "release-checklist")
	if err := os.MkdirAll(skDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skBody := "---\nname: release-checklist\ndescription: Steps before shipping\nversion: 1.0.0\n---\n\n# Release\n\n- tests green\n- version bumped\n"
	if err := os.WriteFile(filepath.Join(skDir, "SKILL.md"), []byte(skBody), 0o644); err != nil {
		t.Fatal(err)
	}
	// linked reference inside the skill dir
	if err := os.WriteFile(filepath.Join(skDir, "steps.md"), []byte("# Detail\n\nstep one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. plugin on disk: a post_tool hook that appends a marker file
	plDir := filepath.Join(dir, ".atria", "plugins", "marker")
	if err := os.MkdirAll(plDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"marker","hooks":{"post_tool":[{"command":"echo marker-fired"}]}}`
	if err := os.WriteFile(filepath.Join(plDir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. subagent definition on disk
	agDir := filepath.Join(dir, ".atria", "agents")
	if err := os.MkdirAll(agDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentMd := "---\nname: scout\ndescription: surveys a codebase\ntools: [view_file, list_dir, grep]\n---\n\nYou are a scout.\n"
	if err := os.WriteFile(filepath.Join(agDir, "scout.md"), []byte(agentMd), 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. MCP server declaration, project-local
	mcpCfg := `{"shout":{"command":"` + serverBin + `","args":[]}}`
	if err := os.WriteFile(filepath.Join(dir, ".atria", "mcp.json"), []byte(mcpCfg), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{CWD: dir, Approval: "full-auto", Model: "test"}

	// skills
	sm := NewSkillManager()
	if problems := sm.Load(cfg); len(problems) > 0 {
		t.Fatalf("skill problems: %v", problems)
	}
	if len(sm.Names()) != 1 {
		t.Fatalf("skills = %v", sm.Names())
	}
	sk, ok := sm.Get("release-checklist")
	if !ok || !contains(sk.Body, "# Release") {
		t.Fatalf("skill body wrong: %q", sk.Body)
	}
	lf, err := sm.loadLinked(sk, "steps.md")
	if err != nil || !contains(lf.Body, "step one") {
		t.Fatalf("linked file wrong: %v %q", err, lf.Body)
	}
	idx := sm.Index()
	if !contains(idx, "release-checklist") {
		t.Fatalf("index missing skill: %q", idx)
	}

	// plugins
	pm := NewPluginManager(dir)
	if problems := pm.Load(); len(problems) > 0 {
		t.Fatalf("plugin problems: %v", problems)
	}
	if len(pm.Names()) != 1 {
		t.Fatalf("plugins = %v", pm.Names())
	}
	res := pm.Fire(context.Background(), EventPostTool, PluginPayload{Tool: "view_file", ToolResult: "x"})
	if res.Block {
		t.Fatal("marker plugin must not block")
	}

	// mcp: real subprocess, real initialize, real list, real call
	m := NewMCPManager()
	if err := m.Load(cfg); err != nil {
		t.Fatal(err)
	}
	problems := m.Connect(context.Background())
	if len(problems) > 0 {
		t.Fatalf("mcp connect problems: %v", problems)
	}
	schemas := m.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("expected 1 mcp tool, got %d", len(schemas))
	}
	full := schemas[0].Function.Name
	if full != "mcp__shout__shout" {
		t.Fatalf("tool name = %q", full)
	}
	if !m.Has(full) {
		t.Fatal("Has() should find the mcp tool")
	}
	out, err := m.Call(context.Background(), full, map[string]any{"text": "hello world"})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if !contains(out, "HELLO WORLD!") {
		t.Fatalf("shout result wrong: %q", out)
	}
	// required-arg validation
	if _, err := m.Call(context.Background(), full, map[string]any{}); err == nil {
		t.Fatal("missing required arg must error")
	}
	m.Close()

	// subagent scope
	a := NewAgent(cfg)
	if problems := a.subs.Load(cfg); len(problems) > 0 {
		t.Fatalf("subagent problems: %v", problems)
	}
	if len(a.subs.Names()) != 1 {
		t.Fatalf("agents = %v", a.subs.Names())
	}
	def, ok := a.subs.defs["scout"]
	if !ok {
		t.Fatal("scout def missing")
	}
	child := a.scopeFor(def, "")
	toolset := map[string]bool{}
	for _, ts := range child.tools {
		toolset[ts.Function.Name] = true
	}
	if !toolset["view_file"] || !toolset["list_dir"] || !toolset["grep"] || toolset["write_file"] {
		t.Fatalf("scout tool set wrong: %v", toolset)
	}
	if !contains(child.messages[0].Content, "You are a scout.") {
		t.Fatalf("scout prompt wrong: %q", child.messages[0].Content)
	}

	// The assembled parent agent: merged tool list and system prompt sections.
	sys := a.messages[0].Content
	if !contains(sys, "Available skills") || !contains(sys, "release-checklist") {
		t.Fatalf("system prompt missing skill index")
	}
	if !contains(sys, "MCP tools") || !contains(sys, "mcp__shout__shout") {
		t.Fatalf("system prompt missing MCP tool list")
	}
	if !contains(sys, "Subagents") || !contains(sys, "scout") {
		t.Fatalf("system prompt missing subagent list")
	}
	// built-in tools + the one MCP tool
	seen := map[string]bool{}
	for _, ts := range a.tools {
		seen[ts.Function.Name] = true
	}
	if !seen["agent"] || !seen["skill"] || !seen["bash"] || !seen["mcp__shout__shout"] {
		t.Fatalf("merged tool list wrong: %v", seen)
	}
	if len(a.tools) != len(a.toolset())+1 {
		t.Fatalf("tool count = %d (built-in %d + 1 mcp)", len(a.tools), len(a.toolset()))
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
