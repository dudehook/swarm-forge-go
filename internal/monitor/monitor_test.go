package monitor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func handoffFile(id, from, to, priority, task, state string) string {
	stamp := ""
	switch state {
	case "in_process":
		stamp = "dequeued_at: 2026-07-01T00:00:02Z\n"
	case "completed":
		stamp = "completed_at: 2026-07-01T00:00:03Z\n"
	default:
		stamp = "enqueued_at: 2026-07-01T00:00:01Z\n"
	}
	return "id: " + id + "\nfrom: " + from + "\nto: " + to +
		"\npriority: " + priority + "\ntype: git_handoff\ntask: " + task + "\n" + stamp +
		"\nmerge_and_process " + from + " 0123456789\n"
}

func TestReadSnapshot(t *testing.T) {
	root := t.TempDir()
	// Two roles, both sharing root as worktree path for simplicity.
	writeFile(t, filepath.Join(root, ".swarmforge", "roles.tsv"),
		"coder\tmaster\t"+root+"\tswarmforge-coder\tCoder\tclaude\ttask\n"+
			"cleaner\tcleaner\t"+root+"\tswarmforge-cleaner\tCleaner\tclaude\tbatch\n")
	writeFile(t, filepath.Join(root, ".swarmforge", "tmux-socket"), "/tmp/none.sock\n")
	// A live daemon pid (this test process).
	writeFile(t, filepath.Join(root, ".swarmforge", "daemon", "handoffd.pid"),
		itoa(os.Getpid())+"\n")
	// One queued, one in-process, one completed handoff.
	writeFile(t, filepath.Join(root, ".swarmforge/handoffs/inbox/new/50_a.handoff"),
		handoffFile("id-a", "coder", "cleaner", "50", "task-a", "new"))
	writeFile(t, filepath.Join(root, ".swarmforge/handoffs/inbox/in_process/50_b.handoff"),
		handoffFile("id-b", "coder", "cleaner", "50", "task-b", "in_process"))
	writeFile(t, filepath.Join(root, ".swarmforge/handoffs/inbox/completed/50_c.handoff"),
		handoffFile("id-c", "coder", "cleaner", "50", "task-c", "completed"))
	// A configured tools list: one present (sh), one absent.
	writeFile(t, filepath.Join(root, "swarmforge", "tools.tsv"),
		"shell\tsh\nnope\tdefinitely-not-a-real-binary-xyz\n")

	snap, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}

	if snap.Project != filepath.Base(root) {
		t.Errorf("project = %q", snap.Project)
	}
	if !snap.Daemon.Running {
		t.Error("daemon should read as running (own pid)")
	}
	if len(snap.Agents) != 2 {
		t.Fatalf("got %d agents, want 2", len(snap.Agents))
	}
	coder := snap.Agents[0]
	if coder.Role != "coder" || coder.Alive {
		t.Errorf("coder = %+v (Alive should be false, no tmux)", coder)
	}
	// Both roles point at the same worktree, so each sees the same inbox counts.
	if coder.New != 1 || coder.InProcess != 1 || coder.Completed != 1 {
		t.Errorf("counts = new %d, in_process %d, completed %d", coder.New, coder.InProcess, coder.Completed)
	}
	if coder.Current != "task-b" {
		t.Errorf("current = %q, want task-b", coder.Current)
	}

	// Messages present with parsed fields; newest (completed) first by time.
	if len(snap.Messages) == 0 {
		t.Fatal("no messages")
	}
	if snap.Messages[0].Task != "task-c" {
		t.Errorf("newest message task = %q, want task-c", snap.Messages[0].Task)
	}
	var sawTaskA bool
	for _, m := range snap.Messages {
		if m.Task == "task-a" {
			sawTaskA = true
			if m.From != "coder" || m.To != "cleaner" || m.State != "new" || m.Priority != "50" {
				t.Errorf("task-a message wrong: %+v", m)
			}
		}
	}
	if !sawTaskA {
		t.Error("task-a message missing")
	}

	// Tools: configured list honored, availability checked.
	byLabel := map[string]bool{}
	for _, tl := range snap.Tools {
		byLabel[tl.Label] = tl.Available
	}
	if !byLabel["shell"] {
		t.Error("shell (sh) should be available")
	}
	if byLabel["nope"] {
		t.Error("nope should be unavailable")
	}
}

func TestDefaultToolsWhenNoFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".swarmforge", "roles.tsv"),
		"coder\tmaster\t"+root+"\ts\tCoder\tclaude\ttask\n")
	snap, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Tools) != len(defaultTools) {
		t.Errorf("got %d tools, want default %d", len(snap.Tools), len(defaultTools))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
