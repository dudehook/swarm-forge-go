// Package monitor reads a running swarm's state from the filesystem and tmux
// into a Snapshot for display. It has no TUI dependency, so it can be tested and
// reused independently of the rendering layer.
package monitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dudehook/swarmforge/internal/handoff"
	"github.com/dudehook/swarmforge/internal/project"
)

// Snapshot is a point-in-time view of a swarm.
type Snapshot struct {
	Project  string
	Root     string
	Socket   string
	Daemon   DaemonStatus
	Agents   []Agent
	Messages []Message
	Tools    []Tool
}

// DaemonStatus describes the handoff daemon.
type DaemonStatus struct {
	Running bool
	PID     int
}

// Agent is one role's live status.
type Agent struct {
	Role      string
	Backend   string
	Worktree  string
	Session   string
	Display   string
	Mode      string
	Alive     bool
	Current   string // task name currently in process, "" if idle
	New       int
	InProcess int
	Completed int
}

// Message is one handoff seen in a recipient's mailbox (or the failed area).
type Message struct {
	ID       string
	From     string
	To       string
	Type     string
	Task     string
	Priority string
	Commit   string
	State    string // new | in_process | completed | failed
	When     time.Time
	File     string
}

// Tool is a checked external tool and whether it is on PATH.
type Tool struct {
	Label     string
	Command   string
	Available bool
}

// defaultTools are the quality tools the coding templates reference. Projects
// can override this list with swarmforge/tools.tsv ("label<TAB>command" lines).
var defaultTools = []Tool{
	{Label: "CRAP", Command: "crap"},
	{Label: "DRY", Command: "dry"},
	{Label: "mutation", Command: "mutation"},
	{Label: "coverage", Command: "coverage"},
	{Label: "ir-dry-checker", Command: "ir-dry-checker"},
}

// Read gathers a Snapshot for the swarm rooted at root.
func Read(root string) (*Snapshot, error) {
	snap := &Snapshot{Project: filepath.Base(root), Root: root}
	snap.Socket = readTrimmed(filepath.Join(root, ".swarmforge", "tmux-socket"))
	snap.Daemon = readDaemon(root)
	snap.Tools = readTools(root)

	rows, err := project.Rows(root)
	if err != nil {
		return nil, err
	}
	alive := aliveSessions(snap.Socket)

	for _, row := range rows {
		if row.Name() == "" {
			continue
		}
		worktreePath := field(row, 2)
		a := Agent{
			Role:     row.Name(),
			Worktree: row.WorktreeName(),
			Session:  field(row, 3),
			Display:  field(row, 4),
			Backend:  field(row, 5),
			Mode:     row.ReceiveMode(),
			Alive:    alive[field(row, 3)],
		}
		inbox := filepath.Join(worktreePath, ".swarmforge", "handoffs", "inbox")
		a.New = len(handoffFiles(filepath.Join(inbox, "new")))
		inProc := collectInbox(filepath.Join(inbox, "in_process"))
		a.InProcess = len(inProc)
		a.Completed = len(collectInbox(filepath.Join(inbox, "completed")))
		if len(inProc) > 0 {
			a.Current = taskName(inProc[0])
		}
		snap.Agents = append(snap.Agents, a)

		// Messages: the recipient-side copies plus anything that failed.
		for state, dir := range map[string]string{
			"new":        filepath.Join(inbox, "new"),
			"in_process": filepath.Join(inbox, "in_process"),
			"completed":  filepath.Join(inbox, "completed"),
			"failed":     filepath.Join(worktreePath, ".swarmforge", "handoffs", "failed"),
		} {
			for _, f := range collectInbox(dir) {
				snap.Messages = append(snap.Messages, readMessage(f, state))
			}
		}
	}

	sort.Slice(snap.Messages, func(i, j int) bool {
		return snap.Messages[i].When.After(snap.Messages[j].When)
	})
	return snap, nil
}

func readDaemon(root string) DaemonStatus {
	pidStr := readTrimmed(filepath.Join(root, ".swarmforge", "daemon", "handoffd.pid"))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return DaemonStatus{}
	}
	return DaemonStatus{Running: syscall.Kill(pid, 0) == nil, PID: pid}
}

func readTools(root string) []Tool {
	tools := loadToolsFile(filepath.Join(root, "swarmforge", "tools.tsv"))
	if tools == nil {
		tools = append([]Tool(nil), defaultTools...)
	}
	for i := range tools {
		_, err := exec.LookPath(tools[i].Command)
		tools[i].Available = err == nil
	}
	return tools
}

func loadToolsFile(path string) []Tool {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var tools []Tool
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		label := parts[0]
		cmd := label
		if len(parts) == 2 {
			cmd = strings.TrimSpace(parts[1])
		}
		tools = append(tools, Tool{Label: label, Command: cmd})
	}
	return tools
}

// aliveSessions returns the set of tmux session names currently running on the
// socket.
func aliveSessions(socket string) map[string]bool {
	set := map[string]bool{}
	if socket == "" {
		return set
	}
	out, err := exec.Command("tmux", "-S", socket, "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return set
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name != "" {
			set[name] = true
		}
	}
	return set
}

// collectInbox returns the .handoff files directly in dir plus those inside any
// batch_ subdirectories.
func collectInbox(dir string) []string {
	files := handoffFiles(dir)
	batches, _ := handoff.BatchDirs(dir)
	for _, b := range batches {
		files = append(files, handoffFiles(b)...)
	}
	return files
}

func handoffFiles(dir string) []string {
	files, _ := handoff.Files(dir)
	return files
}

func taskName(file string) string {
	if v, ok, _ := handoff.FileHeader(file, "task"); ok {
		return v
	}
	return filepath.Base(file)
}

func readMessage(file, state string) Message {
	m := Message{File: file, State: state}
	m.ID, _, _ = headerOr(file, "id")
	m.From, _, _ = headerOr(file, "from")
	m.To, _, _ = headerOr(file, "to")
	m.Type, _, _ = headerOr(file, "type")
	m.Task, _, _ = headerOr(file, "task")
	m.Priority, _, _ = headerOr(file, "priority")
	m.Commit, _, _ = headerOr(file, "commit")
	m.When = messageTime(file, state)
	return m
}

// messageTime picks the timestamp most relevant to the message's current state,
// falling back to file modification time.
func messageTime(file, state string) time.Time {
	field := map[string]string{
		"completed":  "completed_at",
		"in_process": "dequeued_at",
		"new":        "enqueued_at",
	}[state]
	for _, h := range []string{field, "created_at"} {
		if h == "" {
			continue
		}
		if v, ok, _ := handoff.FileHeader(file, h); ok {
			if t, err := parseTime(v); err == nil {
				return t
			}
		}
	}
	if fi, err := os.Stat(file); err == nil {
		return fi.ModTime()
	}
	return time.Time{}
}

func parseTime(v string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, v)
}

func headerOr(file, name string) (string, bool, error) {
	return handoff.FileHeader(file, name)
}

func field(r project.Row, i int) string {
	if i < len(r.Fields) {
		return r.Fields[i]
	}
	return ""
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
