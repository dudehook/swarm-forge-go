// Package tui renders a read-only Bubble Tea dashboard for a running swarm:
// an info panel, a per-agent status panel, a scrolling mailbox-activity feed,
// and a tool-availability checklist. It polls internal/monitor on a ticker.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dudehook/swarmforge/internal/monitor"
)

const refreshInterval = time.Second

var (
	titleStyle = lipgloss.NewStyle().Bold(true)
	boxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
	dimStyle   = lipgloss.NewStyle().Faint(true)
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	badStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	headerBar  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
)

type tickMsg time.Time

type snapshotMsg struct {
	snap *monitor.Snapshot
	err  error
}

type model struct {
	root   string
	snap   *monitor.Snapshot
	err    error
	vp     viewport.Model
	width  int
	height int
	ready  bool
}

// Run starts the dashboard for the swarm rooted at root and blocks until quit.
func Run(root string) error {
	_, err := tea.NewProgram(model{root: root}, tea.WithAltScreen()).Run()
	return err
}

func (m model) Init() tea.Cmd {
	return tea.Batch(loadCmd(m.root), tickCmd())
}

func tickCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func loadCmd(root string) tea.Cmd {
	return func() tea.Msg {
		snap, err := monitor.Read(root)
		return snapshotMsg{snap: snap, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
		var cmd tea.Cmd
		m.vp, cmd = m.vp.Update(msg)
		return m, cmd
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
		m.ready = true
		return m, nil
	case tickMsg:
		return m, tea.Batch(loadCmd(m.root), tickCmd())
	case snapshotMsg:
		m.snap, m.err = msg.snap, msg.err
		m.relayout()
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// relayout recomputes the mailbox viewport size from the current window size and
// the fixed panels, then refreshes its content.
func (m *model) relayout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	top := lipgloss.Height(m.renderHeader()) +
		lipgloss.Height(m.renderTopRow()) +
		lipgloss.Height(m.renderTools()) +
		lipgloss.Height(m.renderFooter())
	// The mailbox box border/title consume a few more lines.
	mailboxHeight := m.height - top - 3
	if mailboxHeight < 1 {
		mailboxHeight = 1
	}
	innerWidth := m.width - 2
	if innerWidth < 1 {
		innerWidth = 1
	}
	if !m.ready {
		m.vp = viewport.New(innerWidth, mailboxHeight)
	} else {
		m.vp.Width = innerWidth
		m.vp.Height = mailboxHeight
	}
	m.vp.SetContent(m.mailboxContent())
}

func (m model) View() string {
	if !m.ready {
		return "Loading swarm…"
	}
	mailbox := boxStyle.Width(m.width - 2).Render(
		titleStyle.Render("Mailbox activity") + "\n" + m.vp.View())
	return strings.Join([]string{
		m.renderHeader(),
		m.renderTopRow(),
		mailbox,
		m.renderTools(),
		m.renderFooter(),
	}, "\n")
}

func (m model) renderHeader() string {
	project, extra := "—", ""
	if m.snap != nil {
		project = m.snap.Project
		extra = fmt.Sprintf("  ·  %d agents", len(m.snap.Agents))
	}
	return headerBar.Render("SwarmForge") + "  " + dimStyle.Render("project: "+project+extra)
}

func (m model) renderTopRow() string {
	leftW := m.width * 2 / 5
	if leftW < 20 {
		leftW = 20
	}
	rightW := m.width - leftW - 2
	if rightW < 20 {
		rightW = 20
	}
	info := boxStyle.Width(leftW).Render(titleStyle.Render("Info") + "\n" + m.infoBody())
	agents := boxStyle.Width(rightW).Render(titleStyle.Render("Agents") + "\n" + m.agentsBody())
	return lipgloss.JoinHorizontal(lipgloss.Top, info, agents)
}

func (m model) infoBody() string {
	if m.snap == nil {
		return dimStyle.Render("no swarm state")
	}
	s := m.snap
	daemon := badStyle.Render("stopped")
	if s.Daemon.Running {
		daemon = okStyle.Render(fmt.Sprintf("running (pid %d)", s.Daemon.PID))
	}
	backends := map[string]bool{}
	for _, a := range s.Agents {
		backends[a.Backend] = true
	}
	return strings.Join([]string{
		"agent:  " + strings.Join(keys(backends), ", "),
		"socket: " + shorten(s.Socket, 24),
		"daemon: " + daemon,
	}, "\n")
}

func (m model) agentsBody() string {
	if m.snap == nil || len(m.snap.Agents) == 0 {
		return dimStyle.Render("no agents")
	}
	var lines []string
	for _, a := range m.snap.Agents {
		dot := badStyle.Render("○")
		if a.Alive {
			dot = okStyle.Render("●")
		}
		status := dimStyle.Render("idle")
		if a.Current != "" {
			status = warnStyle.Render("working ") + a.Current
		}
		mode := ""
		if a.Mode == "batch" {
			mode = dimStyle.Render(" (batch)")
		}
		queue := dimStyle.Render(fmt.Sprintf("  [%d/%d/%d]", a.New, a.InProcess, a.Completed))
		lines = append(lines, fmt.Sprintf("%s %-10s %s%s%s", dot, a.Role, status, mode, queue))
	}
	return strings.Join(lines, "\n")
}

func (m model) mailboxContent() string {
	if m.snap == nil || len(m.snap.Messages) == 0 {
		return dimStyle.Render("no messages yet")
	}
	var lines []string
	for _, msg := range m.snap.Messages {
		when := "        "
		if !msg.When.IsZero() {
			when = msg.When.Format("15:04:05")
		}
		route := fmt.Sprintf("%s→%s", msg.From, msg.To)
		task := msg.Task
		if task == "" {
			task = msg.Type
		}
		lines = append(lines, fmt.Sprintf("%s  %-22s [%s] %-11s %-24s %s",
			dimStyle.Render(when), route, msg.Priority, msg.Type, task, stateBadge(msg.State)))
	}
	return strings.Join(lines, "\n")
}

func stateBadge(state string) string {
	switch state {
	case "completed":
		return okStyle.Render("✓ done")
	case "in_process":
		return warnStyle.Render("▶ active")
	case "failed":
		return badStyle.Render("✗ FAILED")
	default:
		return dimStyle.Render("• queued")
	}
}

func (m model) renderTools() string {
	if m.snap == nil || len(m.snap.Tools) == 0 {
		return ""
	}
	var parts []string
	for _, t := range m.snap.Tools {
		mark := badStyle.Render("✗")
		if t.Available {
			mark = okStyle.Render("✓")
		}
		parts = append(parts, t.Label+" "+mark)
	}
	return boxStyle.Width(m.width - 2).Render(titleStyle.Render("Tools") + "  " + strings.Join(parts, "   "))
}

func (m model) renderFooter() string {
	help := "↑/↓ scroll · q quit"
	if m.err != nil {
		return badStyle.Render("error: "+m.err.Error()) + "   " + dimStyle.Render(help)
	}
	return dimStyle.Render(help)
}

func shorten(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 1 {
		return s[:max]
	}
	return "…" + s[len(s)-max+1:]
}

func keys(m map[string]bool) []string {
	var out []string
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}
