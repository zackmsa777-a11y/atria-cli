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
	input   string    // typed text
	textarea userInput // simple inline cursor input

	events   []AgentEvent // streaming output so far
	paletteQ string
	palSel   int
	done     bool
	err      string
	ctx      context.Context
	cancel   context.CancelFunc
	program  *tea.Program
}

type userInput struct {
	value   string
	cursor  bool // blink phase
}

func (u *userInput) View() string {
	if u.cursor {
		return u.value + "▏"
	}
	return u.value + " "
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
		// resume session — placeholder, wired later
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
		m.state = stateRunning
		go m.agent.Run(m.ctx, text, m.eventChan())
		return m, nil
	case "backspace":
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
		return m, nil
	case "esc":
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
	var b strings.Builder

	// welcome panel only when idle at a blank prompt
	if m.state == stateWelcome && len(m.events) == 0 {
		b.WriteString(m.renderWelcome(boxW))
		b.WriteString("\n")
	}
	if m.state == statePalette {
		b.WriteString(m.renderPalette(boxW))
		b.WriteString("\n")
	}
	b.WriteString(m.renderPromptBar(boxW))

	// streaming output above the prompt
	if len(m.events) > 0 {
		b.WriteString("\n")
		for _, ev := range m.events {
			b.WriteString(m.renderEvent(ev))
			b.WriteString("\n")
		}
	}
	if m.done {
		b.WriteString(dim.Render(fmt.Sprintf("  %s tokens: %d+%d",
			m.cfg.Model, m.agent.usage.PromptTokens, m.agent.usage.CompletionTokens)))
		b.WriteString("\n")
	}
	return b.String()
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
	if w < 62 {
		w = 62
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

func (m *model) renderEvent(ev AgentEvent) string {
	switch ev.Kind {
	case "text":
		return ev.Text
	case "tool":
		return cyan.Render("⟡ "+ev.Tool) + dim.Render(" "+ev.Detail)
	case "think":
		return dim.Render(ev.Text)
	case "done":
		return boldWhite.Render(ev.Text)
	case "error":
		return amber.Render("✕ " + ev.Text)
	}
	return ev.Text
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
