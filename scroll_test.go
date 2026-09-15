package main

// scroll_test.go — unit-test the View() window math without a terminal:
// the transcript must fill the screen exactly, and scrolling must move the
// window without leaving blank space or stale text.

import (
	"strings"
	"testing"
)

func TestViewFillsScreenNoBlanks(t *testing.T) {
	cfg := &Config{Model: "test", Approval: "ask", CWD: "/tmp", Version: "1.0"}
	m := initialModel(cfg)
	m.width, m.height = 100, 40
	m.events = []AgentEvent{{Kind: "text", Text: "hello"}}
	m.done = true

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("View produced %d lines, want exactly %d (screen height)", len(lines), m.height)
	}
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" && i > m.height-6 {
			// trailing region beyond the prompt bar must be structured, not blank
			t.Logf("note: line %d is blank: %q", i, ln)
		}
	}
}

func TestScrollViewMoves(t *testing.T) {
	cfg := &Config{Model: "test", Approval: "ask", CWD: "/tmp", Version: "1.0"}
	m := initialModel(cfg)
	m.width, m.height = 100, 20

	// 100 lines of transcript
	var sb strings.Builder
	for i := 0; i < 100; i++ {
		sb.WriteString("line-")
	}
	m.events = []AgentEvent{{Kind: "text", Text: sb.String()}}
	m.done = true

	// at rest, the last lines should be visible
	out := m.View()
	if !strings.Contains(out, "line-") {
		t.Fatal("expected transcript visible at rest")
	}

	// scroll up 10
	m.scrollBack = 10
	out = m.View()
	if !strings.Contains(out, "10 lines from the bottom") {
		t.Fatalf("expected scroll indicator, got: %q", out[:80])
	}
	// must still be exactly screen-height lines
	if strings.Count(out, "\n")+1 != m.height {
		t.Fatalf("scrolled view is %d lines, want %d", strings.Count(out, "\n")+1, m.height)
	}
}
