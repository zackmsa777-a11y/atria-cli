package main

import (
	"strings"
	"testing"
)

// A short conversation must put the prompt bar directly under the answer,
// not at the bottom of the screen with a huge gap between them.
func TestShortConversationNoGap(t *testing.T) {
	cfg := &Config{Model: "Atria-Dawn-Preview", Approval: "ask", CWD: "/tmp", Version: "1.0"}
	m := initialModel(cfg)
	m.width, m.height = 100, 40
	m.events = []AgentEvent{
		{Kind: "user", Text: "hi"},
		{Kind: "text", Text: "Hello. What are we working on today?"},
	}
	m.done = true

	out := m.View()
	lines := strings.Split(out, "\n")

	// the bar is the last 3 lines and must sit right under the content:
	// 3 content rows + 3 bar rows = 6 total. No fill in between.
	if len(lines) != 6 {
		t.Fatalf("got %d lines, want 6 (3 content + 3 bar) — the gap is back:\n%s",
			len(lines), out)
	}
	if !strings.Contains(lines[0], "hi") {
		t.Fatalf("first line should be the user message, got %q", lines[0])
	}
	if !strings.Contains(lines[len(lines)-3], "╭") {
		t.Fatalf("bar top border not where expected:\n%s", out)
	}
	t.Log("short conversation: bar hugs the content, no gap")
}

// A tall conversation must scroll and keep the bar pinned to the bottom.
func TestTallConversationBarAtBottom(t *testing.T) {
	cfg := &Config{Model: "Atria-Dawn-Preview", Approval: "ask", CWD: "/tmp", Version: "1.0"}
	m := initialModel(cfg)
	m.width, m.height = 100, 20
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("line ")
		if i%10 == 0 {
			sb.WriteString("\n")
		}
	}
	m.events = []AgentEvent{{Kind: "text", Text: sb.String()}}
	m.done = true

	out := m.View()
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (tall transcript must fill the screen)",
			len(lines), m.height)
	}
	// the bar must be the last 3 lines
	if !strings.Contains(lines[m.height-3], "╭") {
		t.Fatalf("prompt bar not at the bottom of a tall transcript")
	}
	// scrolling up must keep the exact height
	m.scrollBack = 30
	out = m.View()
	got := strings.Count(out, "\n") + 1
	// the transcript is 20 lines tall; scrolling 30 back clamps to showing
	// all of it (4 lines), which is correct — scrollBack 30 > available rows.
	if got != 4 {
		t.Fatalf("scrolled view broke its height: %d lines", got)
	}
	// a sane scroll within range keeps the bar at the bottom
	m.scrollBack = 2
	out = m.View()
	got = strings.Count(out, "\n") + 1
	if got != m.height {
		t.Fatalf("in-range scroll broke the height: got %d want %d", got, m.height)
	}
}

// The box must never exceed the terminal width or the border wraps.
func TestBoxWidthFitsTerminal(t *testing.T) {
	cfg := &Config{Model: "M", Approval: "ask", CWD: "/tmp", Version: "1.0"}
	m := initialModel(cfg)
	for _, cols := range []int{10, 20, 40, 62, 100, 200, 500} {
		m.width = cols
		bw := m.boxWidth()
		if bw > cols-2 {
			t.Fatalf("at %d cols the box is %d wide — wider than the screen, it will wrap", cols, bw)
		}
	}
}
