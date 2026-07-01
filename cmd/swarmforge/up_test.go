package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestUpLaunchesSwarmAndDownTearsDown exercises the full orchestration path
// (`up --no-attach` then `down`) with a fake agent backend so no real LLM is
// started. It is skipped where tmux is unavailable.
func TestUpLaunchesSwarmAndDownTearsDown(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	root := t.TempDir()

	// Fake agent named "claude" that just idles, so the lead-agent teardown
	// trailer does not fire during the test.
	fakeBin := filepath.Join(root, "fakebin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(fakeBin, "claude"), "#!/bin/sh\nexec sleep 30\n")
	if err := os.Chmod(filepath.Join(fakeBin, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Minimal single-role project using the claude backend on the master worktree.
	writeFile(t, filepath.Join(root, "swarmforge/constitution.prompt"), "Read articles.\n")
	writeFile(t, filepath.Join(root, "swarmforge/swarmforge.conf"), "window coder claude master\n")
	writeFile(t, filepath.Join(root, "swarmforge/roles/coder.prompt"), "coder\n")

	env := map[string]string{
		"PATH":                fakeBin + ":" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM": "1",
		"HOME":                root,
		"GIT_AUTHOR_NAME":     "Test User",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test User",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	}

	socketFile := filepath.Join(root, ".swarmforge", "tmux-socket")
	pidFile := filepath.Join(root, ".swarmforge", "daemon", "handoffd.pid")
	// Guarantee teardown even if a later assertion fails, so a real handoff
	// daemon is never orphaned (which would keep holding a sleep inhibitor).
	t.Cleanup(func() {
		if sock := strings.TrimSpace(readOrEmpty(socketFile)); sock != "" {
			exec.Command("tmux", "-S", sock, "kill-server").Run()
		}
		if pid := strings.TrimSpace(readOrEmpty(pidFile)); pid != "" {
			exec.Command("kill", "-TERM", pid).Run()
		}
	})

	up := run(t, root, env, "up", "--no-attach")
	if up.exit != 0 {
		t.Fatalf("up exit = %d\nstdout: %s\nstderr: %s", up.exit, up.stdout, up.stderr)
	}

	socket := strings.TrimSpace(readOrEmpty(socketFile))
	if socket == "" {
		t.Fatal("tmux-socket file not written")
	}

	// The role's tmux session exists.
	if err := exec.Command("tmux", "-S", socket, "has-session", "-t", "swarmforge-coder").Run(); err != nil {
		t.Errorf("expected session swarmforge-coder to exist: %v", err)
	}
	// PATH shims were written and are executable.
	shim := filepath.Join(root, "swarmforge", "scripts", "ready_for_next.sh")
	if info, err := os.Stat(shim); err != nil {
		t.Errorf("shim not written: %v", err)
	} else if info.Mode()&0o111 == 0 {
		t.Errorf("shim not executable: %v", info.Mode())
	}
	// State files written.
	if !exists(filepath.Join(root, ".swarmforge", "roles.tsv")) {
		t.Error("roles.tsv not written")
	}
	// Daemon is running (it starts asynchronously, so poll briefly).
	var pid string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if pid = strings.TrimSpace(readOrEmpty(pidFile)); pid != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pid == "" {
		t.Fatal("daemon pid file not written")
	}
	if exec.Command("kill", "-0", pid).Run() != nil {
		t.Errorf("daemon pid %s not alive", pid)
	}

	// Tear down.
	down := run(t, root, env, "down")
	if down.exit != 0 {
		t.Fatalf("down exit = %d (stderr: %s)", down.exit, down.stderr)
	}
	time.Sleep(500 * time.Millisecond)
	if exec.Command("tmux", "-S", socket, "has-session", "-t", "swarmforge-coder").Run() == nil {
		t.Error("session should be killed after down")
	}
	if exists(pidFile) {
		t.Error("daemon pid file should be removed after down")
	}
}

func readOrEmpty(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
