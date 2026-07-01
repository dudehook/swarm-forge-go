package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dudehook/swarmforge/internal/monitor"
)

// TestViewRenders drives the model through a resize and a snapshot, then checks
// that View() renders the expected content without panicking. It needs no tty.
func TestViewRenders(t *testing.T) {
	m := model{root: "/x"}

	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(model)

	snap := &monitor.Snapshot{
		Project: "demo",
		Socket:  "/tmp/swarmforge-x/123.sock",
		Daemon:  monitor.DaemonStatus{Running: true, PID: 4242},
		Agents: []monitor.Agent{
			{Role: "coder", Backend: "claude", Session: "swarmforge-coder", Mode: "task", Alive: true, Current: "task-3", New: 0, InProcess: 1, Completed: 2},
			{Role: "cleaner", Backend: "claude", Session: "swarmforge-cleaner", Mode: "batch", Alive: false},
		},
		Messages: []monitor.Message{
			{From: "coder", To: "cleaner", Type: "git_handoff", Task: "task-3-cave", Priority: "50", State: "in_process"},
			{From: "coder", To: "architect", Type: "git_handoff", Task: "task-2-walls", Priority: "50", State: "failed"},
		},
		Tools: []monitor.Tool{
			{Label: "CRAP", Command: "crap", Available: false},
			{Label: "coverage", Command: "coverage", Available: true},
		},
	}
	next, _ = m.Update(snapshotMsg{snap: snap})
	m = next.(model)

	view := m.View()
	for _, want := range []string{
		"SwarmForge", "demo",
		"coder", "cleaner", // agents
		"task-3", "working", "idle",
		"task-3-cave", "task-2-walls", // mailbox
		"CRAP", "coverage", // tools
		"pid 4242",
		"quit", // footer
	} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q\n---\n%s", want, view)
		}
	}
}

func TestViewBeforeReady(t *testing.T) {
	m := model{root: "/x"}
	if got := m.View(); !strings.Contains(got, "Loading") {
		t.Errorf("pre-ready view = %q", got)
	}
}
