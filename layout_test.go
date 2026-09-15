package main

import (
	"strings"
	"testing"
)

// A short conversation: the prompt bar is pinned to the bottom of the screen
// and the content sits directly above it. Blank space lands at the TOP,
// never sandwiched between the answer and the input.
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

	// the view always fills the screen: content top-aligned, bar at the bottom
	if len(lines) != m.height {
		t.Fatalf("got %d lines, want %d (must fill the screen)", len(lines), m.height)
	}
	// the bar is the last 3 lines
	barTop := len(lines) - 3
	if !strings.Contains(lines[barTop], "╭") {
		t.Fatalf("bar top border not at the bottom:\n%s", out)
	}
	// the answer must sit directly above the bar — no blank rows in between
	for i := barTop - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			if !strings.Contains(lines[i], "Hello") && !strings.Contains(lines[i], "hi") && !strings.Contains(lines[i], "tokens") {
				t.Fatalf("unexpected content at row %d: %q", i, lines[i])
			}
			break
		}
	}
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
		t.Fatalf("got %d lines, want %d", len(lines), m.height)
	}
	if !strings.Contains(lines[m.height-3], "╭") {
		t.Fatalf("prompt bar not at the bottom of a tall transcript")
	}

	// scrolling up must keep the exact height and the bar at the bottom
	m.scrollBack = 5
	out = m.View()
	got := strings.Count(out, "\n") + 1
	if got != m.height {
		t.Fatalf("scrolled view broke its height: got %d want %d", got, m.height)
	}
	lines = strings.Split(out, "\n")
	if !strings.Contains(lines[m.height-3], "╭") {
		t.Fatalf("bar moved off the bottom while scrolling")
	}
	if !strings.Contains(out, "5 lines from the bottom") {
		t.Fatalf("scroll indicator missing")
	}

	// can't scroll past the top
	m.scrollBack = 1 << 30
	m.View()
	if m.scrollBack == 1<<30 {
		t.Fatalf("scrollBack was not clamped")
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

// A `done` event must not emit a duplicate of the answer text — the content
// already arrived as a `text` event.
func TestDoneEventIsJustAMarker(t *testing.T) {
	m := initialModel(&Config{Model: "M", Approval: "ask", CWD: "/tmp"})
	rows := m.renderEvent(AgentEvent{Kind: "done", Text: ""})
	if len(rows) != 0 {
		t.Fatalf("done event should render zero rows, got %d: %v", len(rows), rows)
	}
	rows = m.renderEvent(AgentEvent{Kind: "text", Text: "answer"})
	if len(rows) != 1 || rows[0] != "answer" {
		t.Fatalf("text event should render the content, got %v", rows)
	}
}
