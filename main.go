package main

// main.go — entrypoint: resolve config, first-run key prompt, launch TUI.

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config error:", err)
		os.Exit(1)
	}

	// first run only: ask once, persist, never ask again
	if !cfg.HasKey() {
		key := askForKey()
		if strings.TrimSpace(key) == "" {
			fmt.Fprintln(os.Stderr, "no API key. Set ATRIA_API_KEY or run again to enter one.")
			os.Exit(2)
		}
		cfg.APIKey = strings.TrimSpace(key)
		_ = cfg.save()
	}

	// honor --cwd if given
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "--cwd") {
		parts := strings.SplitN(os.Args[1], "=", 2)
		if len(parts) == 2 && parts[1] != "" {
			cfg.CWD = parts[1]
		}
	}

	m := initialModel(cfg)
	p := tea.NewProgram(m, tea.WithAltScreen())
	m.program = p
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "tui error:", err)
		os.Exit(1)
	}
}

// askForKey prompts once on first run. In the TUI this is a plain stdin read
// before bubbletea takes over the screen.
func askForKey() string {
	fmt.Print("First run — enter your Atria API key (stored in ~/.atria/config.json, never asked again): ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && len(line) == 0 {
		return ""
	}
	return strings.TrimSpace(line)
}
