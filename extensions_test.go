package main

// extensions_test.go — coverage for skills, plugins, MCP naming, and the
// subagent scopeFor clone. No network: these exercise the loaders and the
// routing logic against temp directories.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".atria"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// --- skills ---

func TestSkillLoadAndIndex(t *testing.T) {
	dir := tempProject(t)
	skillsDir := filepath.Join(dir, ".atria", "skills", "deploy-guide")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: deploy-guide\ndescription: How to deploy the stack\nversion: 1.2.0\n---\n\n# Deploy\n\n1. build\n2. ship\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := NewSkillManager()
	cfg := &Config{CWD: dir}
	if problems := sm.Load(cfg); len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	names := sm.Names()
	if len(names) != 1 || names[0] != "deploy-guide" {
		t.Fatalf("got %v", names)
	}

	idx := sm.Index()
	if !strings.Contains(idx, "deploy-guide") || !strings.Contains(idx, "How to deploy the stack") {
		t.Fatalf("index missing skill: %q", idx)
	}

	sk, ok := sm.Get("deploy-guide")
	if !ok {
		t.Fatal("skill not found")
	}
	if !strings.Contains(sk.Body, "# Deploy") {
		t.Fatalf("body not parsed: %q", sk.Body)
	}
	if sk.Version != "1.2.0" {
		t.Fatalf("version = %q", sk.Version)
	}
}

func TestSkillProjectShadowsUser(t *testing.T) {
	dir := tempProject(t)
	skillsDir := filepath.Join(dir, ".atria", "skills", "shared")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: shared\ndescription: project copy wins\n---\n\nproject body\n"
	if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSkillManager()
	if err := sm.Load(&Config{CWD: dir}); err != nil {
		t.Fatal(err)
	}
	if len(sm.Names()) != 1 {
		t.Fatalf("expected 1 skill, got %v", sm.Names())
	}
	sk, _ := sm.Get("shared")
	if !strings.Contains(sk.Body, "project body") {
		t.Fatalf("project skill body wrong: %q", sk.Body)
	}
}

func TestSkillMalformedRejected(t *testing.T) {
	dir := tempProject(t)
	bad := filepath.Join(dir, ".atria", "skills", "broken")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	// unterminated frontmatter
	if err := os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("---\nname: broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sm := NewSkillManager()
	problems := sm.Load(&Config{CWD: dir})
	if len(problems) == 0 {
		t.Fatal("expected a problem for malformed frontmatter")
	}
	if len(sm.Names()) != 0 {
		t.Fatalf("malformed skill should not load, got %v", sm.Names())
	}
}

// --- plugins ---

func TestPluginLoadAndFire(t *testing.T) {
	dir := tempProject(t)
	pdir := filepath.Join(dir, ".atria", "plugins", "logger")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"logger","hooks":{"stop":[{"command":"echo done"}]}}`
	if err := os.WriteFile(filepath.Join(pdir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	pm := NewPluginManager(dir)
	if problems := pm.Load(); len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	names := pm.Names()
	if len(names) != 1 || names[0] != "logger" {
		t.Fatalf("got %v", names)
	}

	res := pm.Fire(context.Background(), EventStop, PluginPayload{})
	if res.Block {
		t.Fatal("echo must not block")
	}
}

func TestPluginPreToolCanBlock(t *testing.T) {
	dir := tempProject(t)
	pdir := filepath.Join(dir, ".atria", "plugins", "guard")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// exit 2 on pre_tool blocks, per the Claude Code hook convention
	manifest := `{"name":"guard","hooks":{"pre_tool":[{"command":"exit 2"}]}}`
	if err := os.WriteFile(filepath.Join(pdir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pm := NewPluginManager(dir)
	pm.Load()

	res := pm.Fire(context.Background(), EventPreTool, PluginPayload{Tool: "write_file"})
	if !res.Block {
		t.Fatal("exit 2 on pre_tool must block")
	}
}

// --- MCP naming ---

func TestMCPToolNaming(t *testing.T) {
	full := mcpToolName("github", "create_issue")
	if full != "mcp__github__create_issue" {
		t.Fatalf("got %q", full)
	}
	server, tool, err := splitMCPTool(full)
	if err != nil {
		t.Fatal(err)
	}
	if server != "github" || tool != "create_issue" {
		t.Fatalf("split = %q/%q", server, tool)
	}
	if _, _, err := splitMCPTool("bash"); err == nil {
		t.Fatal("non-mcp tool must not split")
	}
}

func TestMCPContentJoin(t *testing.T) {
	out := joinMCPContent(nil)
	if out != "" {
		t.Fatalf("empty content = %q", out)
	}
}

// --- subagent scoping ---

func TestSubagentScopeFiltersTools(t *testing.T) {
	dir := tempProject(t)
	cfg := &Config{CWD: dir, Approval: "ask", Model: "test-model"}
	a := NewAgent(cfg)

	adir := filepath.Join(dir, ".atria", "agents")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentMd := "---\nname: reviewer\ndescription: reviews code\nmodel: other-model\ntools: [view_file, grep]\n---\n\nYou are a reviewer.\n"
	if err := os.WriteFile(filepath.Join(adir, "reviewer.md"), []byte(agentMd), 0o644); err != nil {
		t.Fatal(err)
	}
	if problems := a.subs.Load(cfg); len(problems) > 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	names := a.subs.Names()
	if len(names) != 1 || names[0] != "reviewer" {
		t.Fatalf("got %v", names)
	}

	def, ok := a.subs.defs["reviewer"]
	if !ok {
		t.Fatal("reviewer def missing")
	}
	child := a.scopeFor(def, "")
	if child.cfg.Model != "other-model" {
		t.Fatalf("model override not applied: %q", child.cfg.Model)
	}
	// filtered tool set: only view_file + grep
	names2 := map[string]bool{}
	for _, t2 := range child.tools {
		names2[t2.Function.Name] = true
	}
	if len(names2) != 2 || !names2["view_file"] || !names2["grep"] {
		t.Fatalf("tools not filtered: %v", names2)
	}
	if !strings.Contains(child.messages[0].Content, "You are a reviewer.") {
		t.Fatalf("system prompt not scoped: %q", child.messages[0].Content)
	}
}

func TestSubagentUnknownRejected(t *testing.T) {
	dir := tempProject(t)
	a := NewAgent(&Config{CWD: dir})
	_, err := a.subs.Run(context.Background(), "nope", "do a thing", "", true)
	if err == nil {
		t.Fatal("unknown subagent must error")
	}
}
