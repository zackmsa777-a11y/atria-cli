package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestReplaceFileContent(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "sample.txt")
	original := "line 1\nreplace this text\nline 3\n"
	if err := os.WriteFile(filePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Successful replacement
	res := replaceFileContent("", filePath, "replace this text", "new text here")
	if strings.HasPrefix(res, "error") {
		t.Fatalf("unexpected error: %s", res)
	}
	content, _ := os.ReadFile(filePath)
	if !strings.Contains(string(content), "new text here") {
		t.Fatalf("expected new text in file, got: %s", string(content))
	}

	// 2. Missing target
	resMissing := replaceFileContent("", filePath, "nonexistent", "other")
	if !strings.Contains(resMissing, "not found") {
		t.Fatalf("expected not found error, got: %s", resMissing)
	}

	// 3. Duplicate target
	dupPath := filepath.Join(dir, "dup.txt")
	_ = os.WriteFile(dupPath, []byte("repeat\nrepeat\n"), 0o644)
	resDup := replaceFileContent("", dupPath, "repeat", "single")
	if !strings.Contains(resDup, "found 2 times") {
		t.Fatalf("expected multiple occurrences error, got: %s", resDup)
	}
}

func TestWindowedReadFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "lines.txt")
	var sb strings.Builder
	for i := 1; i <= 20; i++ {
		sb.WriteString(strings.Repeat("a", 10) + "\n")
	}
	_ = os.WriteFile(filePath, []byte(sb.String()), 0o644)

	// Read lines 5 to 8
	out := readFile("", filePath, 5, 8)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "5 |") {
		t.Fatalf("expected line 5 prefix, got: %s", lines[0])
	}
	if !strings.Contains(lines[3], "8 |") {
		t.Fatalf("expected line 8 prefix, got: %s", lines[3])
	}
}

func TestApprovalFlow(t *testing.T) {
	cfg := &Config{Model: "Atria-Dawn-Preview", Approval: "ask", CWD: "/tmp"}
	m := initialModel(cfg)

	req := &ApprovalRequest{
		Tool:   "bash",
		Detail: "rm -rf /tmp/test",
		Resp:   make(chan ApprovalDecision, 1),
	}

	// Trigger approval event
	m.Update(AgentEvent{Kind: "ask_approval", ApprovalReq: req})
	if m.state != stateApproval {
		t.Fatalf("expected stateApproval, got %v", m.state)
	}

	// Approve once
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	dec := <-req.Resp
	if dec != DecisionAllowOnce {
		t.Fatalf("expected DecisionAllowOnce, got %v", dec)
	}
	if m.state != stateRunning {
		t.Fatalf("expected stateRunning, got %v", m.state)
	}
}

func TestApprovalAllowAlways(t *testing.T) {
	cfg := &Config{Model: "Atria-Dawn-Preview", Approval: "ask", CWD: "/tmp"}
	m := initialModel(cfg)

	req := &ApprovalRequest{
		Tool:   "write_file",
		Detail: "write config",
		Resp:   make(chan ApprovalDecision, 1),
	}

	m.Update(AgentEvent{Kind: "ask_approval", ApprovalReq: req})
	// Press 'a'
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	dec := <-req.Resp
	if dec != DecisionAllowAlways {
		t.Fatalf("expected DecisionAllowAlways, got %v", dec)
	}
	if m.cfg.Approval != "full-auto" {
		t.Fatalf("expected full-auto, got %s", m.cfg.Approval)
	}
}

func TestCycleReasoning(t *testing.T) {
	cfg := &Config{Model: "Atria-Dawn-Preview", Reasoning: "low"}
	if cfg.CycleReasoning() != "medium" {
		t.Fatalf("expected medium")
	}
	if cfg.CycleReasoning() != "high" {
		t.Fatalf("expected high")
	}
	if cfg.CycleReasoning() != "max" {
		t.Fatalf("expected max")
	}
	if cfg.CycleReasoning() != "low" {
		t.Fatalf("expected low")
	}
}
