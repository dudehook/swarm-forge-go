package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests are a faithful port of test/swarmforge/handoff_test.clj. They
// drive the compiled binary the same way the Babashka suite drives the .sh
// scripts: real temp project dirs, real git repos, black-box assertions on exit
// code, stdout/stderr, and the resulting filesystem state.

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "swarmforge-bin.")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	binPath = filepath.Join(dir, "swarmforge")
	build := exec.Command("go", "build", "-o", binPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		panic("failed to build test binary: " + err.Error())
	}
	os.Exit(m.Run())
}

type result struct {
	exit   int
	stdout string
	stderr string
}

// run invokes the binary in dir with a replaced environment (matching the
// Clojure suite, which passes only PATH and GIT_CONFIG_NOSYSTEM plus extras).
func run(t *testing.T, dir string, env map[string]string, args ...string) result {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	baseEnv := map[string]string{
		"PATH":                os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM": "1",
	}
	for k, v := range env {
		baseEnv[k] = v
	}
	envSlice := make([]string, 0, len(baseEnv))
	for k, v := range baseEnv {
		envSlice = append(envSlice, k+"="+v)
	}
	cmd.Env = envSlice
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("failed to run %v: %v", args, err)
		}
	}
	return result{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v failed: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func initRepo(t *testing.T, root string) string {
	t.Helper()
	git(t, root, "init", "-q")
	git(t, root, "config", "user.email", "test@example.com")
	git(t, root, "config", "user.name", "Test User")
	writeFile(t, filepath.Join(root, "README.md"), "initial\n")
	git(t, root, "add", "README.md")
	git(t, root, "commit", "-q", "-m", "Initial commit")
	return git(t, root, "rev-parse", "--short=10", "HEAD")
}

type rolePair struct {
	role string
	mode string
}

func setupProject(t *testing.T, root string, roles ...rolePair) {
	t.Helper()
	if len(roles) == 0 {
		roles = []rolePair{{"sender", "task"}, {"receiver", "task"}}
	}
	for _, d := range []string{
		".swarmforge/handoffs/outbox/tmp",
		".swarmforge/handoffs/sent",
		".swarmforge/handoffs/failed",
		".swarmforge/handoffs/inbox/new",
		".swarmforge/handoffs/inbox/in_process",
		".swarmforge/handoffs/inbox/completed",
	} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	for _, r := range roles {
		// role \t worktree \t root \t session \t display \t agent \t mode
		b.WriteString(r.role + "\tmaster\t" + root + "\tsession\t" +
			strings.Title(r.role) + "\tcodex\t" + r.mode + "\n")
	}
	writeFile(t, filepath.Join(root, ".swarmforge/roles.tsv"), b.String())
}

type handoffAttrs struct {
	id, from, to, recipient, priority, typ, task, commit, body string
	enqueuedAt, dequeuedAt, completedAt                        string
}

func handoffContent(a handoffAttrs) string {
	var b strings.Builder
	b.WriteString("id: " + a.id + "\n")
	b.WriteString("from: " + a.from + "\n")
	b.WriteString("to: " + a.to + "\n")
	if a.recipient != "" {
		b.WriteString("recipient: " + a.recipient + "\n")
	}
	b.WriteString("priority: " + a.priority + "\n")
	b.WriteString("type: " + a.typ + "\n")
	if a.task != "" {
		b.WriteString("task: " + a.task + "\n")
	}
	if a.commit != "" {
		b.WriteString("commit: " + a.commit + "\n")
	}
	if a.enqueuedAt != "" {
		b.WriteString("enqueued_at: " + a.enqueuedAt + "\n")
	}
	if a.dequeuedAt != "" {
		b.WriteString("dequeued_at: " + a.dequeuedAt + "\n")
	}
	if a.completedAt != "" {
		b.WriteString("completed_at: " + a.completedAt + "\n")
	}
	b.WriteString("\n")
	body := a.body
	if body == "" {
		body = "payload for " + a.id
	}
	b.WriteString(body + "\n")
	return b.String()
}

func handoffPath(root, state, filename string) string {
	return filepath.Join(root, ".swarmforge", "handoffs", "inbox", state, filename)
}

func putHandoff(t *testing.T, root, state, filename string, a handoffAttrs) string {
	t.Helper()
	path := handoffPath(root, state, filename)
	writeFile(t, path, handoffContent(a))
	return path
}

// makeQueuedHandoff writes a queued git_handoff in inbox/new with the same
// defaults the Clojure helper uses.
func makeQueuedHandoff(t *testing.T, root, filename string, a handoffAttrs) string {
	t.Helper()
	if a.from == "" {
		a.from = "sender"
	}
	if a.to == "" {
		a.to = "receiver"
	}
	if a.recipient == "" {
		a.recipient = "receiver"
	}
	if a.priority == "" {
		a.priority = "50"
	}
	if a.typ == "" {
		a.typ = "git_handoff"
	}
	if a.task == "" {
		a.task = "task-one"
	}
	if a.commit == "" {
		a.commit = "0123456789"
	}
	if a.body == "" {
		a.body = "merge_and_process sender 0123456789"
	}
	return putHandoff(t, root, "new", filename, a)
}

func header(t *testing.T, path, field string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	prefix := field + ": "
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			break
		}
		if strings.HasPrefix(line, prefix) {
			return line[len(prefix):], true
		}
	}
	return "", false
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func glob(t *testing.T, dir, pattern string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

func TestSwarmHandoffValidatesAndQueuesGitHandoffs(t *testing.T) {
	root := t.TempDir()
	commit := initRepo(t, root)
	setupProject(t, root)

	t.Run("git_handoff requires a task name", func(t *testing.T) {
		draft := filepath.Join(root, "tmp", "missing-task.handoff")
		writeFile(t, draft, "type: git_handoff\nto: receiver\npriority: 50\ncommit: "+commit+"\n")
		res := run(t, root, map[string]string{"SWARMFORGE_ROLE": "sender"}, "handoff", "send", draft)
		if res.exit != 2 {
			t.Fatalf("exit = %d, want 2 (stderr: %s)", res.exit, res.stderr)
		}
		if !strings.Contains(res.stderr, "Missing required header 'task'") {
			t.Fatalf("stderr missing task error: %s", res.stderr)
		}
		if !exists(draft) {
			t.Fatal("draft should still exist after invalid handoff")
		}
	})

	t.Run("valid git_handoff writes task, canonical commit, and payload", func(t *testing.T) {
		draft := filepath.Join(root, "tmp", "valid.handoff")
		writeFile(t, draft, "type: git_handoff\nto: receiver\npriority: 50\ntask: task-1-cave-setup\ncommit: "+commit+"\n")
		res := run(t, root, map[string]string{"SWARMFORGE_ROLE": "sender"}, "handoff", "send", draft)
		if res.exit != 0 {
			t.Fatalf("exit = %d, want 0 (stderr: %s)", res.exit, res.stderr)
		}
		queued := strings.TrimPrefix(strings.TrimSpace(res.stdout), "HANDOFF QUEUED: ")
		content, err := os.ReadFile(queued)
		if err != nil {
			t.Fatalf("read queued: %v", err)
		}
		c := string(content)
		if !strings.Contains(c, "task: task-1-cave-setup\n") {
			t.Errorf("queued missing task header:\n%s", c)
		}
		if !strings.Contains(c, "commit: "+commit+"\n") {
			t.Errorf("queued missing commit header:\n%s", c)
		}
		if !strings.Contains(c, "merge_and_process sender "+commit) {
			t.Errorf("queued missing payload:\n%s", c)
		}
		if exists(draft) {
			t.Error("draft should be removed after successful handoff")
		}
	})
}

func TestReadyForNextTaskAcceptsAndResumes(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	setupProject(t, root, rolePair{"receiver", "task"})

	makeQueuedHandoff(t, root, "50_20260615T000001Z_000001_from_sender_to_receiver.handoff",
		handoffAttrs{id: "20260615T000001Z_000001_from_sender", task: "task-alpha"})
	res := run(t, root, map[string]string{"SWARMFORGE_ROLE": "receiver"}, "ready-for-next")
	if res.exit != 0 {
		t.Fatalf("exit = %d (stderr: %s)", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, "TASK:") {
		t.Errorf("missing TASK: in %s", res.stdout)
	}
	if !strings.Contains(res.stdout, "TASK_NAME: task-alpha") {
		t.Errorf("missing task-alpha in %s", res.stdout)
	}
	inProcess := handoffPath(root, "in_process", "50_20260615T000001Z_000001_from_sender_to_receiver.handoff")
	if !exists(inProcess) {
		t.Fatal("task should be in_process")
	}
	if _, ok := header(t, inProcess, "dequeued_at"); !ok {
		t.Error("in_process task missing dequeued_at")
	}

	t.Run("resumes existing in-process task before queued", func(t *testing.T) {
		makeQueuedHandoff(t, root, "40_20260615T000002Z_000002_from_sender_to_receiver.handoff",
			handoffAttrs{id: "20260615T000002Z_000002_from_sender", priority: "40", task: "task-beta"})
		res := run(t, root, map[string]string{"SWARMFORGE_ROLE": "receiver"}, "ready-for-next")
		if !strings.Contains(res.stdout, "task-alpha") {
			t.Errorf("should resume task-alpha, got %s", res.stdout)
		}
		if !exists(handoffPath(root, "new", "40_20260615T000002Z_000002_from_sender_to_receiver.handoff")) {
			t.Error("queued task-beta should remain in new")
		}
	})
}

func TestReadyForNextBatchGroupsEqualPriority(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	setupProject(t, root, rolePair{"receiver", "batch"})
	makeQueuedHandoff(t, root, "10_20260615T000001Z_000001_from_sender_to_receiver.handoff",
		handoffAttrs{id: "20260615T000001Z_000001_from_sender", priority: "10", task: "task-a"})
	makeQueuedHandoff(t, root, "10_20260615T000002Z_000002_from_sender_to_receiver.handoff",
		handoffAttrs{id: "20260615T000002Z_000002_from_sender", priority: "10", task: "task-b"})
	makeQueuedHandoff(t, root, "20_20260615T000003Z_000003_from_sender_to_receiver.handoff",
		handoffAttrs{id: "20260615T000003Z_000003_from_sender", priority: "20", task: "task-c"})

	res := run(t, root, map[string]string{"SWARMFORGE_ROLE": "receiver"}, "ready-for-next")
	if res.exit != 0 {
		t.Fatalf("exit = %d (stderr: %s)", res.exit, res.stderr)
	}
	var batchDir string
	for _, line := range strings.Split(res.stdout, "\n") {
		if strings.HasPrefix(line, "BATCH: ") {
			batchDir = line[len("BATCH: "):]
		}
	}
	if !strings.Contains(res.stdout, "COUNT: 2") {
		t.Errorf("expected COUNT: 2 in %s", res.stdout)
	}
	if !strings.Contains(res.stdout, "TASK_NAME: task-a") || !strings.Contains(res.stdout, "TASK_NAME: task-b") {
		t.Errorf("expected task-a and task-b in %s", res.stdout)
	}
	if strings.Contains(res.stdout, "TASK_NAME: task-c") {
		t.Errorf("task-c should not be in batch: %s", res.stdout)
	}
	if got := len(glob(t, batchDir, "*.handoff")); got != 2 {
		t.Errorf("batch dir has %d handoffs, want 2", got)
	}
	if !exists(handoffPath(root, "new", "20_20260615T000003Z_000003_from_sender_to_receiver.handoff")) {
		t.Error("priority-20 task should remain queued")
	}
}

func TestDoneWithCurrentTaskCompletesAndAcceptsNext(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	setupProject(t, root, rolePair{"receiver", "task"})
	putHandoff(t, root, "in_process", "50_20260615T000001Z_000001_from_sender_to_receiver.handoff",
		handoffAttrs{id: "20260615T000001Z_000001_from_sender", from: "sender", to: "receiver",
			recipient: "receiver", priority: "50", typ: "git_handoff", task: "task-current", commit: "0123456789"})
	makeQueuedHandoff(t, root, "50_20260615T000002Z_000002_from_sender_to_receiver.handoff",
		handoffAttrs{id: "20260615T000002Z_000002_from_sender", task: "task-next"})

	res := run(t, root, map[string]string{"SWARMFORGE_ROLE": "receiver"}, "done-with-current")
	if res.exit != 0 {
		t.Fatalf("exit = %d (stderr: %s)", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, "COMPLETED:") {
		t.Errorf("expected COMPLETED: in %s", res.stdout)
	}
	if !strings.Contains(res.stdout, "TASK_NAME: task-next") {
		t.Errorf("expected task-next in %s", res.stdout)
	}
	completed := handoffPath(root, "completed", "50_20260615T000001Z_000001_from_sender_to_receiver.handoff")
	if _, ok := header(t, completed, "completed_at"); !ok {
		t.Error("completed task missing completed_at")
	}
	next := handoffPath(root, "in_process", "50_20260615T000002Z_000002_from_sender_to_receiver.handoff")
	if _, ok := header(t, next, "dequeued_at"); !ok {
		t.Error("next task missing dequeued_at")
	}
}

func TestDoneWithCurrentBatchCompletesAndAcceptsNext(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	setupProject(t, root, rolePair{"receiver", "batch"})
	batch := handoffPath(root, "in_process", "batch_20260615T000001Z_000001")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(batch, "10_20260615T000001Z_000001_from_sender_to_receiver.handoff"),
		handoffContent(handoffAttrs{id: "20260615T000001Z_000001_from_sender", from: "sender", to: "receiver",
			recipient: "receiver", priority: "10", typ: "git_handoff", task: "task-a", commit: "0123456789"}))
	writeFile(t, filepath.Join(batch, "10_20260615T000002Z_000002_from_sender_to_receiver.handoff"),
		handoffContent(handoffAttrs{id: "20260615T000002Z_000002_from_sender", from: "sender", to: "receiver",
			recipient: "receiver", priority: "10", typ: "git_handoff", task: "task-b", commit: "0123456789"}))
	makeQueuedHandoff(t, root, "20_20260615T000003Z_000003_from_sender_to_receiver.handoff",
		handoffAttrs{id: "20260615T000003Z_000003_from_sender", priority: "20", task: "task-c"})

	res := run(t, root, map[string]string{"SWARMFORGE_ROLE": "receiver"}, "done-with-current")
	if res.exit != 0 {
		t.Fatalf("exit = %d (stderr: %s)", res.exit, res.stderr)
	}
	if !strings.Contains(res.stdout, "COMPLETED_BATCH:") {
		t.Errorf("expected COMPLETED_BATCH: in %s", res.stdout)
	}
	if !strings.Contains(res.stdout, "TASK_NAME: task-c") {
		t.Errorf("expected task-c in %s", res.stdout)
	}
	completedBatch := handoffPath(root, "completed", "batch_20260615T000001Z_000001")
	files := glob(t, completedBatch, "*.handoff")
	if len(files) != 2 {
		t.Errorf("completed batch has %d files, want 2", len(files))
	}
	for _, f := range files {
		if _, ok := header(t, f, "completed_at"); !ok {
			t.Errorf("%s missing completed_at", f)
		}
	}
}

func TestHelpersRefuseWrongCurrentWorkShape(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	setupProject(t, root, rolePair{"receiver", "batch"})
	batch := handoffPath(root, "in_process", "batch_20260615T000001Z_000001")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(batch, "10_20260615T000001Z_000001_from_sender_to_receiver.handoff"),
		handoffContent(handoffAttrs{id: "20260615T000001Z_000001_from_sender", from: "sender", to: "receiver",
			recipient: "receiver", priority: "10", typ: "git_handoff", task: "task-a", commit: "0123456789"}))

	ready := run(t, root, map[string]string{"SWARMFORGE_ROLE": "receiver"}, "ready-for-next-task")
	if ready.exit != 2 {
		t.Errorf("ready_for_next_task exit = %d, want 2", ready.exit)
	}
	if !strings.Contains(ready.stderr, "TASK_IN_PROCESS_IS_BATCH") {
		t.Errorf("expected TASK_IN_PROCESS_IS_BATCH, got %s", ready.stderr)
	}
	done := run(t, root, map[string]string{"SWARMFORGE_ROLE": "receiver"}, "done-with-current-task")
	if done.exit != 2 {
		t.Errorf("done_with_current_task exit = %d, want 2", done.exit)
	}
	if !strings.Contains(done.stderr, "CURRENT_WORK_IS_BATCH") {
		t.Errorf("expected CURRENT_WORK_IS_BATCH, got %s", done.stderr)
	}
}
