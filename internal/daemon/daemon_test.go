package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDaemonExitsWhenStateDirRemoved verifies the daemon self-terminates (rather
// than spinning forever, holding a sleep inhibitor) once its .swarmforge state
// directory disappears — e.g. after the project is deleted.
func TestDaemonExitsWhenStateDirRemoved(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, ".swarmforge")
	if err := os.MkdirAll(filepath.Join(stateDir, "daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(stateDir, "roles.tsv"),
		"coder\tmaster\t"+root+"\tswarmforge-coder\tCoder\tclaude\ttask\n")
	writeFile(t, filepath.Join(stateDir, "tmux-socket"), "/tmp/none-xyz.sock\n")

	d := New(root)
	done := make(chan error, 1)
	go func() { done <- d.Run() }()

	// Wait for the daemon to come up (pid file written).
	pidFile := filepath.Join(stateDir, "daemon", "handoffd.pid")
	if !waitFor(func() bool { return exists(pidFile) }, 3*time.Second) {
		t.Fatal("daemon did not start (no pid file)")
	}

	// Delete the project's state directory out from under it.
	if err := os.RemoveAll(stateDir); err != nil {
		t.Fatal(err)
	}

	// The daemon should notice within a poll cycle and exit on its own.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not exit after state directory was removed")
	}
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

func waitFor(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
