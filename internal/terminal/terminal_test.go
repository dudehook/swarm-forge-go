package terminal

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttachArgsAlacritty(t *testing.T) {
	args := attachArgs("alacritty", "/tmp/s.sock", Window{Title: "SwarmForge Coder", Session: "swarmforge-coder"})
	got := strings.Join(args, " ")
	want := "--title SwarmForge Coder --class swarmforge,swarmforge-coder -e tmux -S /tmp/s.sock attach -t swarmforge-coder"
	if got != want {
		t.Errorf("alacritty args:\n got %q\nwant %q", got, want)
	}
}

func TestAttachArgsFallback(t *testing.T) {
	args := attachArgs("xterm", "/tmp/s.sock", Window{Title: "x", Session: "swarmforge-cleaner"})
	got := strings.Join(args, " ")
	want := "-e tmux -S /tmp/s.sock attach -t swarmforge-cleaner"
	if got != want {
		t.Errorf("fallback args:\n got %q\nwant %q", got, want)
	}
}

// TestOpenWindowsSpawnsPerSession points SWARMFORGE_TERMINAL at a stub that
// records its args, so we can verify one detached invocation per session
// without a real GUI.
func TestOpenWindowsSpawnsPerSession(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "calls.log")
	stub := filepath.Join(dir, "faketerm")
	script := "#!/bin/sh\necho \"$@\" >> " + logFile + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SWARMFORGE_TERMINAL", stub)
	// Ensure the stub resolves via LookPath.
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	var out bytes.Buffer
	windows := []Window{
		{Title: "SwarmForge Coder", Session: "swarmforge-coder"},
		{Title: "SwarmForge Cleaner", Session: "swarmforge-cleaner"},
	}
	if err := OpenWindows(&out, "/tmp/s.sock", windows); err != nil {
		t.Fatalf("OpenWindows: %v", err)
	}

	// Detached spawns; poll for both lines.
	var data []byte
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		data, _ = os.ReadFile(logFile)
		if strings.Count(string(data), "attach -t") >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	s := string(data)
	if !strings.Contains(s, "attach -t swarmforge-coder") {
		t.Errorf("missing coder attach in calls:\n%s", s)
	}
	if !strings.Contains(s, "attach -t swarmforge-cleaner") {
		t.Errorf("missing cleaner attach in calls:\n%s", s)
	}
}
