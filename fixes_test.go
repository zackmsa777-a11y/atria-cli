package main

import (
	"context"
	"testing"
	tea "github.com/charmbracelet/bubbletea"
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

	if m.turnCtx.Err() != nil {
		t.Fatalf("expected new turn context to be active and not cancelled, got: %v", m.turnCtx.Err())
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
