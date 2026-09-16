package main

import (
	"context"
	"strings"
	tea "github.com/charmbracelet/bubbletea"
	"testing"
)

// TestNarrowTerminalPromptBar ensures renderPromptBar does not panic on very narrow terminals.
func TestNarrowTerminalPromptBar(t *testing.T) {
	cfg := &Config{Model: "Atria-Dawn-Preview", Approval: "ask", CWD: "/tmp", Version: "1.0"}
	m := initialModel(cfg)

	widths := []int{2, 4, 8, 12, 16, 20, 25, 30, 35, 40, 80, 120}
	for _, w := range widths {
		m.width = w
		bw := m.boxWidth()
		bar := m.renderPromptBar(bw)
		if len(bar) == 0 {
			t.Fatalf("empty prompt bar at width %d", w)
		}
	}
}

// TestPaletteFiltering ensures filtering palette commands matches properly.
func TestPaletteFiltering(t *testing.T) {
	cfg := &Config{Model: "M", Approval: "ask", CWD: "/tmp"}
	m := initialModel(cfg)

	m.paletteQ = "yol"
	shown := m.filteredPalette()
	if len(shown) != 1 || shown[0].name != "yolo" {
		t.Fatalf("expected 1 result (yolo), got %d: %v", len(shown), shown)
	}

	m.paletteQ = "tool"
	shown = m.filteredPalette()
	if len(shown) != 1 || shown[0].name != "tools" {
		t.Fatalf("expected 1 result (tools), got %d: %v", len(shown), shown)
	}

	m.paletteQ = "nonexistent"
	shown = m.filteredPalette()
	if len(shown) != 0 {
		t.Fatalf("expected 0 results, got %d", len(shown))
	}
}

// TestUnicodeInput verifies non-ASCII runes (Turkish, accented, etc.) can be typed into input.
func TestUnicodeInput(t *testing.T) {
	cfg := &Config{Model: "M", Approval: "ask", CWD: "/tmp"}
	m := initialModel(cfg)

	// Simulate typing Turkish character 'ş'
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ş")})
	// Simulate typing 'ü'
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ü")})
	// Simulate typing space
	m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	// Simulate typing 'test'
	m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("test")})

	expected := "şü test"
	if m.input != expected {
		t.Fatalf("expected input %q, got %q", expected, m.input)
	}
}

// TestTurnCancellationRecovery verifies that cancelling a turn does not break subsequent turns.
func TestTurnCancellationRecovery(t *testing.T) {
	cfg := &Config{Model: "M", Approval: "ask", CWD: "/tmp"}
	m := initialModel(cfg)

	// Setup a cancelled turn
	m.turnCtx, m.turnCancel = context.WithCancel(context.Background())
	m.turnCancel()

	if m.turnCtx.Err() == nil {
		t.Fatalf("expected context to be cancelled")
	}

	// Now enter a new prompt
	m.input = "new task"
	m.promptKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.turnCancel != nil {
		m.turnCancel()
	}

	if m.turnCtx.Err() == nil {
		t.Fatalf("expected turn to cancel cleanly")
	}
}

// TestResolvePath verifies path resolution respects workspace CWD.
func TestResolvePath(t *testing.T) {
	cwd := "/home/user/project"
	rel := resolvePath(cwd, "file.txt")
	if rel != "/home/user/project/file.txt" {
		t.Fatalf("expected /home/user/project/file.txt, got %q", rel)
	}

	abs := resolvePath(cwd, "/var/log/syslog")
	if abs != "/var/log/syslog" {
		t.Fatalf("expected /var/log/syslog, got %q", abs)
	}
}

// TestConversationHistoryPreserved verifies that entering prompts appends to events rather than wiping history.
func TestConversationHistoryPreserved(t *testing.T) {
	cfg := &Config{Model: "M", Approval: "ask", CWD: "/tmp"}
	m := initialModel(cfg)
	m.width, m.height = 100, 40

	// Turn 1
	m.input = "question 1"
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.turnCancel != nil {
		m.turnCancel()
	}
	m.events = append(m.events, AgentEvent{Kind: "text", Text: "answer 1"})
	m.state = stateWelcome
	m.done = true

	if len(m.events) < 2 {
		t.Fatalf("expected at least 2 events after turn 1, got %d", len(m.events))
	}
	if m.events[0].Kind != "user" || m.events[0].Text != "question 1" {
		t.Fatalf("expected first event to be user prompt 'question 1', got %v", m.events[0])
	}

	// Turn 2
	m.input = "question 2"
	m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if m.turnCancel != nil {
		m.turnCancel()
	}
	m.events = append(m.events, AgentEvent{Kind: "text", Text: "answer 2"})
	m.state = stateWelcome
	m.done = true

	// Turn 1 events must still be present!
	if len(m.events) < 4 {
		t.Fatalf("expected history from turn 1 and turn 2 to be preserved (at least 4 events), got %d", len(m.events))
	}
	if m.events[0].Text != "question 1" || m.events[2].Text != "question 2" {
		t.Fatalf("conversation history was lost across turns: %v", m.events)
	}

	// View must contain both questions
	out := m.View()
	if !strings.Contains(out, "question 1") || !strings.Contains(out, "question 2") {
		t.Fatalf("expected View to contain both questions, got:\n%s", out)
	}
}

// TestThinkingSpinnerAnimation verifies that tickMsg increments spinIdx and changes spinner frames.
func TestThinkingSpinnerAnimation(t *testing.T) {
	cfg := &Config{Model: "M", Approval: "ask", CWD: "/tmp"}
	m := initialModel(cfg)
	m.state = stateRunning

	initialIdx := m.spinIdx
	bar1 := m.renderPromptBar(80)

	// Dispatch tick
	modelUpdated, cmd := m.Update(tickMsg{})
	m = modelUpdated.(*model)

	if m.spinIdx != initialIdx+1 {
		t.Fatalf("expected spinIdx to increment to %d, got %d", initialIdx+1, m.spinIdx)
	}
	if cmd == nil {
		t.Fatalf("expected next tick cmd to be returned while stateRunning")
	}

	bar2 := m.renderPromptBar(80)
	if bar1 == bar2 {
		t.Fatalf("expected spinner frame to change across ticks, but prompt bars are identical:\n%s", bar1)
	}
}
