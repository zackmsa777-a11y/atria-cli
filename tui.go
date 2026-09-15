package main

// tui.go — Bubble Tea UI: welcome panel, prompt bar, command palette,
// streaming output, all rendered via Lip Gloss.

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---- styles ----

var (
	borderDim   = lipgloss.NewStyle().BorderForeground(lipgloss.Color("#2E2E32"))
	dim         = lipgloss.NewStyle().Foreground(lipgloss.Color("#71717A"))
	amber       = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5A93C"))
	cyan        = lipgloss.NewStyle().Foreground(lipgloss.Color("#22D3EE"))
	boldWhite   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF"))
	selStyle    = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("#242426")).Foreground(lipgloss.Color("#FFFFFF"))
	promptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#22D3EE")).Bold(true)
)

// stippled Atria mark
var logoLines = []string{
	"          .::::::::.          ",
	"        .::::::::::::.        ",
	"       .:::::    ':::::.      ",
	"      .:::::::.    '::::.     ",
	"     .::::::'       '::::.    ",
	"    .::::::           ::::    ",
	"  .::::::             ':::::.",
	" .::::::               ':::::",
	" :::::::                ::::::",
	".:::::::::::::::::.      :::::",
	" '::::::::::::::::::::::::::' ",
	"   ':::::::'  ':::::::::::'   ",
}

// ---- the model ----

type sessionState int

const (
	stateWelcome sessionState = iota
	stateRunning
	statePalette
)

type model struct {
	cfg     *Config
	agent   *Agent
	state   sessionState
	width   int
	height  int
	input   string

	events     []AgentEvent // streaming output so far
	paletteQ   string
	palSel     int
	done       bool
	err        string
	ctx        context.Context
	cancel     context.CancelFunc
	program    *tea.Program
	scrollBack int // lines scrolled up from the bottom (0 = pinned to latest)
}



func initialModel(cfg *Config) *model {
	ctx, cancel := context.WithCancel(context.Background())
	return &model{
		cfg:    cfg,
		agent:  NewAgent(cfg),
		state:  stateWelcome,
		ctx:    ctx,
		cancel: cancel,
	}
}

// ---- tea.Model impl ----

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case AgentEvent:
		m.events = append(m.events, msg)
		if msg.Kind == "done" || msg.Kind == "error" {
			m.done = true
			m.state = stateWelcome
		}
		return m, nil
	}
	return m, nil
}

func (m *model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// global
	switch k.String() {
	case "ctrl+c", "ctrl+q":
		m.cancel()
		return m, tea.Quit
	case "ctrl+r":
		return m, nil
	case "ctrl+w":
		return m, nil
	}

	switch m.state {
	case statePalette:
		return m.paletteKey(k)
	case stateRunning:
		// during a turn, only cancel is live
		if k.String() == "esc" || k.String() == "ctrl+c" {
			m.cancel()
		}
		return m, nil
	default: // stateWelcome == prompt
		return m.promptKey(k)
	}
}

func (m *model) promptKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// scrolling the transcript — arrows move the cursor in a real editor, but
	// here (single-line input) they scroll, which is what you expect when
	// reaching for old output.
	switch k.String() {
	case "up", "pgup":
		n := 1
		if k.String() == "pgup" {
			n = 10
		}
		m.scrollBack += n
		return m, nil
	case "down", "pgdown":
		n := 1
		if k.String() == "pgdown" {
			n = 10
		}
		m.scrollBack -= n
		if m.scrollBack < 0 {
			m.scrollBack = 0
		}
		return m, nil
	case "home":
		// jump to the top of the transcript
		m.scrollBack = 1 << 30
		return m, nil
	case "end":
		m.scrollBack = 0
		return m, nil
	}

	switch k.String() {
	case "/":
		m.state = statePalette
		m.paletteQ = ""
		m.palSel = 0
		return m, nil
	case "enter":
		text := strings.TrimSpace(m.input)
		if text == "" {
			return m, nil
		}
		m.input = ""
		m.done = false
		m.events = nil
		m.scrollBack = 0
		m.state = stateRunning
		go m.agent.Run(m.ctx, text, m.eventChan())
		return m, nil
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case "esc":
		if m.scrollBack > 0 {
			m.scrollBack = 0
			return m, nil
		}
		m.input = ""
		return m, nil
	}
	if len(k.String()) == 1 {
		m.input += k.String()
	}
	return m, nil
}

func (m *model) paletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmds := paletteCommands()
	switch k.String() {
	case "esc", "ctrl+c":
		m.state = stateWelcome
		return m, nil
	case "enter":
		if len(cmds) > 0 {
			c := cmds[m.palSel%len(cmds)]
			m.state = stateWelcome
			mm, cmd := c.fn(m)
			return mm, cmd
		}
		m.state = stateWelcome
		return m, nil
	case "j", "down":
		if len(cmds) > 0 {
			m.palSel = (m.palSel + 1) % len(cmds)
		}
		return m, nil
	case "k", "up":
		if len(cmds) > 0 {
			m.palSel = (m.palSel - 1 + len(cmds)) % len(cmds)
		}
		return m, nil
	case "backspace":
		if len(m.paletteQ) > 0 {
			m.paletteQ = m.paletteQ[:len(m.paletteQ)-1]
		} else {
			m.state = stateWelcome
		}
		return m, nil
	}
	if len(k.String()) == 1 {
		m.paletteQ += k.String()
		m.palSel = 0
	}
	return m, nil
}

// eventChan returns a buffered channel that the agent pushes to. A goroutine
// forwards each event into the program as an AgentEvent message.
func (m *model) eventChan() chan<- AgentEvent {
	ch := make(chan AgentEvent, 64)
	go func() {
		for ev := range ch {
			m.program.Send(ev)
		}
	}()
	return ch
}

// ---- rendering ----

func (m *model) View() string {
	boxW := m.boxWidth()
	promptBar := m.renderPromptBar(boxW)

	// the transcript: palette when open, else welcome panel, else the event log
	var rows []string
	switch {
	case m.state == statePalette:
		rows = strings.Split(m.renderPalette(boxW), "\n")
	case len(m.events) == 0 && m.state == stateWelcome:
		rows = strings.Split(m.renderWelcome(boxW), "\n")
	default:
		for _, ev := range m.events {
			rows = append(rows, m.renderEvent(ev)...)
		}
		if m.done {
			rows = append(rows, dim.Render(fmt.Sprintf("  %s tokens: %d+%d",
				m.cfg.Model, m.agent.usage.PromptTokens, m.agent.usage.CompletionTokens)))
		}
	}

	// the prompt bar always occupies the final 3 rows
	const barH = 3
	avail := m.height - barH
	if avail < 1 {
		avail = 1
	}

	// scroll window: scrollBack==0 pins to the latest output
	end := len(rows) - m.scrollBack
	if end > len(rows) {
		end = len(rows)
	}
	if end < 0 {
		end = 0
	}
	start := end - avail
	if start < 0 {
		start = 0
	}
	// clamp: never scroll past the top of the transcript
	if m.scrollBack > len(rows) {
		m.scrollBack = len(rows)
		if m.scrollBack < 0 {
			m.scrollBack = 0
		}
	}
	window := rows[start:end]

	if m.scrollBack > 0 {
		indicator := amber.Render(fmt.Sprintf("↑ %d lines from the bottom — ↓/PgDn to return",
			m.scrollBack))
		// pin the indicator at the top of the visible region
		if len(window) > 0 {
			window[0] = indicator
		} else {
			window = append(window, indicator)
		}
	}

	// The prompt bar is pinned to the bottom of the screen, OpenCode-style:
	// the transcript grows upward from just above it, and any leftover blank
	// space lands at the TOP of the screen (where it reads as margin) rather
	// than sandwiched between the answer and the input.
	if len(window) > avail {
		window = window[len(window)-avail:]
	} else if len(window) < avail {
		// pad on top so the content sits directly above the bar
		fill := make([]string, avail-len(window))
		window = append(fill, window...)
	}

	return strings.Join(window, "\n") + "\n" + promptBar
}

// renderWelcome draws the bordered panel: stippled logo left, identity+menu right.
func (m *model) renderWelcome(boxW int) string {
	menu := []string{
		boldWhite.Render("Atria Build ") + dim.Render("v"+m.cfg.Version),
		"",
		dim.Render(fmt.Sprintf("%s · ready", m.cfg.Model)),
		"",
		dim.Render("workspace ") + cyan.Render(m.cfg.CWD),
		dim.Render("endpoint ") + cyan.Render(m.cfg.ProviderTag()),
		dim.Render("approval ") + cyan.Render(m.cfg.Approval),
		"",
		boldWhite.Render("New worktree ") + dim.Render("ctrl+w"),
		boldWhite.Render("Resume session ") + dim.Render("ctrl+r"),
		boldWhite.Render("Changelog ") + dim.Render("/changelog"),
		boldWhite.Render("Quit ") + dim.Render("ctrl+q"),
	}

	n := len(logoLines)
	if len(menu) > n {
		n = len(menu)
	}
	logoW := len(logoLines[0])

	var rows []string
	for i := 0; i < n; i++ {
		var left, right string
		if i < len(logoLines) {
			left = logoLines[i]
		}
		if i < len(menu) {
			right = menu[i]
		}
		pad := logoW - len(left)
		if pad < 0 {
			pad = 0
		}
		rows = append(rows, dim.Render(left+strings.Repeat(" ", pad)+"   ")+right)
	}

	style := borderDim.Border(lipgloss.RoundedBorder()).Width(boxW)
	return dim.Render("~") + "\n" + style.Render(strings.Join(rows, "\n"))
}

func (m *model) boxWidth() int {
	w := m.width
	if w <= 0 {
		w = 80
	}
	if w > 91 {
		w = 91
	}
	// never wider than the actual screen: an over-wide box wraps its border
	// and mangles the prompt bar. Shrink instead.
	if w < 4 {
		w = 4
	}
	return w - 2
}

// renderPromptBar draws the boxed input bar. The model badge is embedded in
// the bottom border. Three lines built manually so the badge sits inside the
// border instead of adding an extra row.
func (m *model) renderPromptBar(boxW int) string {
	label := fmt.Sprintf(" %s · %s ", m.cfg.Model, m.cfg.Approval)
	labelW := lipgloss.Width(label)
	leftRule := strings.Repeat("─", boxW-labelW-1)

	var content string
	if m.state == stateRunning {
		content = amber.Render(" ⠋ working… (esc to cancel) ")
	} else {
		content = promptStyle.Render("> ") + m.input + "▏"
	}
	contentW := lipgloss.Width(content)
	pad := boxW - contentW
	if pad < 0 {
		pad = 0
	}

	top := dim.Render("╭" + strings.Repeat("─", boxW) + "╮")
	mid := dim.Render("│") + content + strings.Repeat(" ", pad) + dim.Render("│")
	bot := dim.Render("╰"+leftRule) + dim.Render(label) + dim.Render("─╯")
	return top + "\n" + mid + "\n" + bot
}

// renderPalette draws the slash command overlay.
func (m *model) renderPalette(boxW int) string {
	all := paletteCommands()
	q := strings.ToLower(m.paletteQ)
	var shown []paletteCmd
	for _, c := range all {
		if q == "" || strings.Contains(strings.ToLower(c.name), q) {
			shown = append(shown, c)
		}
	}
	var rows []string
	for i, c := range shown {
		line := fmt.Sprintf(" /%-16s %s", c.name, c.desc)
		if i == m.palSel%max(1, len(shown)) {
			rows = append(rows, selStyle.Render(line))
		} else {
			rows = append(rows, dim.Render("  "+line))
		}
	}
	if len(shown) == 0 {
		rows = append(rows, dim.Render("  no match — backspace to clear"))
	}
	footer := dim.Render("  Enter: run  │  j/k: navigate  │  esc: close")
	return strings.Join(rows, "\n") + "\n" + footer + "\n"
}

func (m *model) renderEvent(ev AgentEvent) []string {
	switch ev.Kind {
	case "text":
		return strings.Split(ev.Text, "\n")
	case "tool":
		return []string{cyan.Render("⟡ "+ev.Tool) + dim.Render(" "+ev.Detail)}
	case "think":
		return strings.Split(dim.Render(ev.Text), "\n")
	case "done":
		// marker only — the answer arrived as a `text` event
		return nil
	case "error":
		return []string{amber.Render("✕ " + ev.Text)}
	}
	return []string{ev.Text}
}

// ---- palette commands ----

type paletteCmd struct {
	name string
	desc string
	fn   func(*model) (tea.Model, tea.Cmd)
}

func paletteCommands() []paletteCmd {
	return []paletteCmd{
		{"help", "show the command list", func(m *model) (tea.Model, tea.Cmd) {
			m.events = []AgentEvent{{Kind: "text", Text: m.helpText()}}
			return m, nil
		}},
		{"clear", "wipe conversation history", func(m *model) (tea.Model, tea.Cmd) {
			m.agent.messages = []Message{{Role: "system", Content: m.agent.systemPrompt()}}
			m.events = nil
			m.done = true
			return m, nil
		}},
		{"yolo", "toggle no-approvals mode", func(m *model) (tea.Model, tea.Cmd) {
			if m.cfg.Approval == "full-auto" {
				m.cfg.Approval = "ask"
			} else {
				m.cfg.Approval = "full-auto"
			}
			return m, nil
		}},
		{"tools", "show enabled tools", func(m *model) (tea.Model, tea.Cmd) {
			names := make([]string, 0, len(m.agent.tools))
			for _, t := range m.agent.tools {
				names = append(names, t.Function.Name)
			}
			m.events = []AgentEvent{{Kind: "text", Text: fmt.Sprintf("%d tools: %s", len(names), strings.Join(names, ", "))}}
			m.done = true
			return m, nil
		}},
		{"exit", "leave the REPL", func(m *model) (tea.Model, tea.Cmd) {
			return m, tea.Quit
		}},
	}
}

func (m *model) helpText() string {
	return strings.Join([]string{
		"Atria commands:",
		"  /help      show this list",
		"  /yolo      toggle no-approvals mode",
		"  /tools     show enabled tools",
		"  /clear     wipe history",
		"  /exit      leave",
	}, "\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
