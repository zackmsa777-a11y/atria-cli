package main

// tui.go — Bubble Tea UI: welcome panel, prompt bar, command palette,
// live SSE streaming, approval modal, and diff rendering via Lip Gloss.

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ---- styles ----

var (
	borderDim   = lipgloss.NewStyle().BorderForeground(lipgloss.Color("#2E2E32"))
	borderWarn  = lipgloss.NewStyle().BorderForeground(lipgloss.Color("#E5A93C"))
	dim         = lipgloss.NewStyle().Foreground(lipgloss.Color("#71717A"))
	amber       = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5A93C"))
	green       = lipgloss.NewStyle().Foreground(lipgloss.Color("#4ADE80"))
	red         = lipgloss.NewStyle().Foreground(lipgloss.Color("#F87171"))
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
	stateApproval
)

// spinner animation frames
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type tickMsg time.Time

func tickSpinner() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

type model struct {
	cfg    *Config
	agent  *Agent
	state  sessionState
	width  int
	height int
	input  string
	usage  Usage

	events      []AgentEvent // streaming output so far
	paletteQ    string
	palSel      int
	done        bool
	err         string
	turnCtx     context.Context
	turnCancel  context.CancelFunc
	program     *tea.Program
	scrollBack  int // lines scrolled up from the bottom (0 = pinned to latest)
	curApproval *ApprovalRequest
	spinIdx     int

	// extension subsystems — the same instances the agent owns
	mcp     *MCPManager
	skills  *SkillManager
	plugins *PluginManager
	subs    *SubagentManager
}

func initialModel(cfg *Config) *model {
	a := NewAgent(cfg)
	return &model{
		cfg:     cfg,
		agent:   a,
		state:   stateWelcome,
		mcp:     a.mcp,
		skills:  a.skills,
		plugins: a.plugins,
		subs:    a.subs,
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

	case tickMsg:
		if m.state == stateRunning {
			m.spinIdx++
			return m, tickSpinner()
		}
		return m, nil

	case AgentEvent:
		m.usage = msg.Usage
		switch msg.Kind {
		case "delta":
			if len(m.events) > 0 && m.events[len(m.events)-1].Kind == "text" {
				m.events[len(m.events)-1].Text += msg.Text
			} else {
				m.events = append(m.events, AgentEvent{Kind: "text", Text: msg.Text})
			}
			return m, nil

		case "think_delta":
			if len(m.events) > 0 && m.events[len(m.events)-1].Kind == "think" {
				m.events[len(m.events)-1].Text += msg.Text
			} else {
				m.events = append(m.events, AgentEvent{Kind: "think", Text: msg.Text})
			}
			return m, nil

		case "ask_approval":
			m.state = stateApproval
			m.curApproval = msg.ApprovalReq
			return m, nil

		case "done", "error":
			if msg.Kind == "error" {
				m.events = append(m.events, msg)
			}
			m.done = true
			m.state = stateWelcome
			m.curApproval = nil
			return m, nil

		default:
			m.events = append(m.events, msg)
			return m, nil
		}
	}
	return m, nil
}

func (m *model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// global
	switch k.String() {
	case "ctrl+c", "ctrl+q":
		if m.curApproval != nil {
			m.curApproval.Resp <- DecisionDeny
			m.curApproval = nil
		}
		if m.turnCancel != nil {
			m.turnCancel()
		}
		return m, tea.Quit
	case "ctrl+r":
		return m, nil
	case "ctrl+w":
		return m, nil
	}

	switch m.state {
	case stateApproval:
		return m.approvalKey(k)
	case statePalette:
		return m.paletteKey(k)
	case stateRunning:
		if k.String() == "esc" || k.String() == "ctrl+c" {
			if m.turnCancel != nil {
				m.turnCancel()
			}
		}
		return m, nil
	default: // stateWelcome == prompt
		return m.promptKey(k)
	}
}

func (m *model) approvalKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.curApproval == nil {
		m.state = stateRunning
		return m, nil
	}

	switch strings.ToLower(k.String()) {
	case "y", "enter":
		m.curApproval.Resp <- DecisionAllowOnce
		m.curApproval = nil
		m.state = stateRunning
		return m, tickSpinner()
	case "a":
		m.curApproval.Resp <- DecisionAllowAlways
		m.cfg.Approval = "full-auto"
		_ = m.cfg.save()
		m.curApproval = nil
		m.state = stateRunning
		return m, tickSpinner()
	case "n", "esc":
		m.curApproval.Resp <- DecisionDeny
		m.curApproval = nil
		m.state = stateRunning
		return m, tickSpinner()
	}
	return m, nil
}

func (m *model) promptKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		m.events = append(m.events, AgentEvent{Kind: "user", Text: text})
		m.scrollBack = 0
		m.state = stateRunning
		if m.turnCancel != nil {
			m.turnCancel()
		}
		m.turnCtx, m.turnCancel = context.WithCancel(context.Background())
		go m.agent.Run(m.turnCtx, text, m.eventChan())
		return m, tickSpinner()
	case "backspace":
		if len(m.input) > 0 {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
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

	switch k.Type {
	case tea.KeyRunes:
		m.input += string(k.Runes)
		return m, nil
	case tea.KeySpace:
		m.input += " "
		return m, nil
	}
	if len(k.Runes) > 0 {
		m.input += string(k.Runes)
		return m, nil
	}
	if len(k.String()) == 1 {
		m.input += k.String()
	}
	return m, nil
}

func (m *model) filteredPalette() []paletteCmd {
	all := paletteCommands()
	q := strings.ToLower(m.paletteQ)
	if q == "" {
		return all
	}
	var shown []paletteCmd
	for _, c := range all {
		if strings.Contains(strings.ToLower(c.name), q) {
			shown = append(shown, c)
		}
	}
	return shown
}

func (m *model) paletteKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmds := m.filteredPalette()
	switch k.String() {
	case "esc", "ctrl+c":
		m.state = stateWelcome
		return m, nil
	case "enter":
		m.state = stateWelcome
		if len(cmds) > 0 {
			c := cmds[m.palSel%len(cmds)]
			mm, cmd := c.fn(m)
			return mm, cmd
		}
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
			r := []rune(m.paletteQ)
			m.paletteQ = string(r[:len(r)-1])
			m.palSel = 0
		} else {
			m.state = stateWelcome
		}
		return m, nil
	}
	if len(k.Runes) > 0 {
		m.paletteQ += string(k.Runes)
		m.palSel = 0
	} else if len(k.String()) == 1 {
		m.paletteQ += k.String()
		m.palSel = 0
	}
	return m, nil
}

func (m *model) eventChan() chan<- AgentEvent {
	ch := make(chan AgentEvent, 128)
	go func() {
		for ev := range ch {
			if m.program != nil {
				m.program.Send(ev)
			}
		}
	}()
	return ch
}

// ---- rendering ----

func (m *model) View() string {
	boxW := m.boxWidth()
	promptBar := m.renderPromptBar(boxW)

	var rows []string
	switch {
	case m.state == stateApproval:
		for _, ev := range m.events {
			rows = append(rows, m.renderEvent(ev)...)
		}
		rows = append(rows, strings.Split(m.renderApproval(boxW), "\n")...)
	case m.state == statePalette:
		rows = strings.Split(m.renderPalette(boxW), "\n")
	case len(m.events) == 0 && m.state == stateWelcome:
		rows = strings.Split(m.renderWelcome(boxW), "\n")
	default:
		for _, ev := range m.events {
			rows = append(rows, m.renderEvent(ev)...)
		}
		if m.done {
			used := m.usage.PromptTokens + m.usage.CompletionTokens
			pct := float64(used) / 256000.0 * 100.0
			rows = append(rows, dim.Render(fmt.Sprintf("  %s · %d tokens (%.1f%% of 256k)",
				m.cfg.Model, used, pct)))
		}
	}

	const barH = 3
	avail := m.height - barH
	if avail < 1 {
		avail = 1
	}

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
		if len(window) > 0 {
			window[0] = indicator
		} else {
			window = append(window, indicator)
		}
	}

	if len(window) > avail {
		window = window[len(window)-avail:]
	} else if len(window) < avail {
		fill := make([]string, avail-len(window))
		window = append(fill, window...)
	}

	return strings.Join(window, "\n") + "\n" + promptBar
}

func (m *model) renderApproval(boxW int) string {
	tool := "action"
	detail := ""
	if m.curApproval != nil {
		tool = m.curApproval.Tool
		detail = m.curApproval.Detail
	}
	lines := []string{
		amber.Render("⚠ Action Approval Required:"),
		"",
		cyan.Render("  Tool:   ") + boldWhite.Render(tool),
		dim.Render("  Detail: ") + boldWhite.Render(truncate(detail, 80)),
		"",
		boldWhite.Render("  [y] ") + dim.Render("Approve once") + "   " +
			boldWhite.Render("[a] ") + dim.Render("Always allow (YOLO)") + "   " +
			boldWhite.Render("[n] ") + dim.Render("Deny"),
	}
	style := borderWarn.Border(lipgloss.RoundedBorder()).Width(boxW)
	return style.Render(strings.Join(lines, "\n"))
}

func (m *model) renderWelcome(boxW int) string {
	menu := []string{
		boldWhite.Render("Atria Build ") + dim.Render("v"+m.cfg.Version),
		"",
		dim.Render(fmt.Sprintf("%s · ready", m.cfg.Model)),
		dim.Render("reasoning ") + cyan.Render(m.cfg.Reasoning),
		"",
		dim.Render("workspace ") + cyan.Render(m.cfg.CWD),
		dim.Render("endpoint ") + cyan.Render(m.cfg.ProviderTag()),
		dim.Render("approval ") + cyan.Render(m.cfg.Approval),
		"",
		boldWhite.Render("Undo last turn ") + dim.Render("/undo"),
		boldWhite.Render("View Git diff ") + dim.Render("/diff"),
		boldWhite.Render("Cycle reasoning ") + dim.Render("/effort"),
		boldWhite.Render("Toggle approvals ") + dim.Render("/yolo"),
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
	if w < 4 {
		w = 4
	}
	return w - 2
}

func (m *model) renderPromptBar(boxW int) string {
	used := m.usage.PromptTokens + m.usage.CompletionTokens
	label := fmt.Sprintf(" %s · %s · 256k [%s] ", m.cfg.Model, m.cfg.Approval, m.cfg.Reasoning)
	if used > 0 {
		label = fmt.Sprintf(" %s · 256k: %d · %s ", m.cfg.Model, used, m.cfg.Reasoning)
	}
	labelW := lipgloss.Width(label)
	ruleCount := boxW - labelW - 1
	if ruleCount < 0 {
		ruleCount = 0
	}
	leftRule := strings.Repeat("─", ruleCount)

	var content string
	if m.state == stateRunning {
		frame := spinFrames[m.spinIdx%len(spinFrames)]
		content = amber.Render(fmt.Sprintf(" %s Atria thinking… (esc to cancel) ", frame))
	} else if m.state == stateApproval {
		content = amber.Render(" ⚠ Waiting for your approval [y/a/n] ")
	} else {
		content = promptStyle.Render("> ") + m.input + "▏"
	}
	contentW := lipgloss.Width(content)
	pad := boxW - contentW
	if pad < 0 {
		pad = 0
	}

	top := dim.Render("╭" + strings.Repeat("─", max(0, boxW)) + "╮")
	mid := dim.Render("│") + content + strings.Repeat(" ", pad) + dim.Render("│")
	var bot string
	if boxW >= labelW+2 {
		bot = dim.Render("╰"+leftRule) + dim.Render(label) + dim.Render("─╯")
	} else {
		bot = dim.Render("╰" + strings.Repeat("─", max(0, boxW)) + "╯")
	}
	return top + "\n" + mid + "\n" + bot
}

func (m *model) renderPalette(boxW int) string {
	shown := m.filteredPalette()
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
		lines := strings.Split(ev.Text, "\n")
		var out []string
		for _, l := range lines {
			if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
				out = append(out, green.Render(l))
			} else if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") {
				out = append(out, red.Render(l))
			} else if strings.HasPrefix(l, "@@") {
				out = append(out, cyan.Render(l))
			} else {
				out = append(out, l)
			}
		}
		return out
	case "tool":
		return []string{cyan.Render("⟡ "+ev.Tool) + dim.Render(" "+truncate(ev.Detail, 100))}
	case "think":
		return strings.Split(dim.Render("💭 "+ev.Text), "\n")
	case "done":
		return nil
	case "user":
		return []string{
			"",
			promptStyle.Render("> ") + boldWhite.Render(ev.Text),
			"",
		}
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
			m.done = true
			return m, nil
		}},
		{"effort", "cycle reasoning effort (low/medium/high)", func(m *model) (tea.Model, tea.Cmd) {
			eff := m.cfg.CycleReasoning()
			m.events = []AgentEvent{{Kind: "text", Text: fmt.Sprintf("Reasoning effort set to: %s", eff)}}
			m.done = true
			return m, nil
		}},
		{"diff", "show unstaged git changes", func(m *model) (tea.Model, tea.Cmd) {
			out, err := exec.Command("git", "diff").CombinedOutput()
			if err != nil || len(strings.TrimSpace(string(out))) == 0 {
				m.events = []AgentEvent{{Kind: "text", Text: "No unstaged git changes."}}
			} else {
				m.events = []AgentEvent{{Kind: "text", Text: string(out)}}
			}
			m.done = true
			return m, nil
		}},
		{"undo", "revert unstaged changes since last turn", func(m *model) (tea.Model, tea.Cmd) {
			_ = exec.Command("git", "checkout", "--", ".").Run()
			m.events = []AgentEvent{{Kind: "text", Text: "✓ Workspace changes reverted via git checkout."}}
			m.done = true
			return m, nil
		}},
		{"yolo", "toggle no-approvals mode", func(m *model) (tea.Model, tea.Cmd) {
			if m.cfg.Approval == "full-auto" {
				m.cfg.Approval = "ask"
			} else {
				m.cfg.Approval = "full-auto"
			}
			_ = m.cfg.save()
			m.events = []AgentEvent{{Kind: "text", Text: fmt.Sprintf("Approval mode set to: %s", m.cfg.Approval)}}
			m.done = true
			return m, nil
		}},
		{"agents", "list declared subagent types", func(m *model) (tea.Model, tea.Cmd) {
			names := m.subs.Names()
			if len(names) == 0 {
				m.events = []AgentEvent{{Kind: "text", Text: "No agents declared. Add ~/.atria/agents/<name>.md or .atria/agents/<name>.md."}}
			} else {
				var b strings.Builder
				b.WriteString("Declared subagents:\n")
				for _, n := range names {
					fmt.Fprintf(&b, "  %s\n", n)
				}
				m.events = []AgentEvent{{Kind: "text", Text: b.String()}}
			}
			return m, nil
		}},
		{"skills", "list loaded skills", func(m *model) (tea.Model, tea.Cmd) {
			names := m.skills.Names()
			if len(names) == 0 {
				m.events = []AgentEvent{{Kind: "text", Text: "No skills loaded. Add ~/.atria/skills/<name>/SKILL.md or .atria/skills/<name>/SKILL.md."}}
			} else {
				var b strings.Builder
				b.WriteString("Loaded skills:\n")
				for _, n := range names {
					fmt.Fprintf(&b, "  %s\n", n)
				}
				m.events = []AgentEvent{{Kind: "text", Text: b.String()}}
			}
			return m, nil
		}},
		{"mcp", "list connected MCP servers and tools", func(m *model) (tea.Model, tea.Cmd) {
			schemas := m.mcp.Schemas()
			if len(schemas) == 0 {
				m.events = []AgentEvent{{Kind: "text", Text: "No MCP servers connected. Declare in ~/.atria/config.json under mcp_servers, or .atria/mcp.json."}}
			} else {
				var b strings.Builder
				b.WriteString("MCP tools:\n")
				for _, sc := range schemas {
					fmt.Fprintf(&b, "  %s\n", sc.Function.Name)
				}
				m.events = []AgentEvent{{Kind: "text", Text: b.String()}}
			}
			return m, nil
		}},
		{"plugins", "list loaded plugins", func(m *model) (tea.Model, tea.Cmd) {
			names := m.plugins.Names()
			if len(names) == 0 {
				m.events = []AgentEvent{{Kind: "text", Text: "No plugins loaded. Add ~/.atria/plugins/<name>/plugin.json or .atria/plugins/<name>/plugin.json."}}
			} else {
				var b strings.Builder
				b.WriteString("Loaded plugins:\n")
				for _, n := range names {
					fmt.Fprintf(&b, "  %s\n", n)
				}
				m.events = []AgentEvent{{Kind: "text", Text: b.String()}}
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
		{"clear", "wipe conversation history", func(m *model) (tea.Model, tea.Cmd) {
			m.agent.messages = []Message{{Role: "system", Content: m.agent.systemPrompt()}}
			m.events = nil
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
		"  /effort    cycle reasoning effort (low/medium/high)",
		"  /diff      view unstaged git diff",
		"  /undo      revert workspace changes",
		"  /yolo      toggle no-approvals mode",
		"  /agents    list subagent types",
		"  /skills    list loaded skills",
		"  /mcp       list MCP tools",
		"  /plugins   list plugins",
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
