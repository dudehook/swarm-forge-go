// Package terminal opens native terminal windows attached to the swarm's tmux
// sessions — one window per agent. This restores the "a window per agent"
// experience (the original had it for macOS/Windows) for Linux terminals,
// defaulting to Alacritty.
package terminal

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// Window describes one terminal window to open for a tmux session.
type Window struct {
	Title   string
	Session string
}

// Terminal returns the terminal emulator command to use: SWARMFORGE_TERMINAL if
// set, otherwise "alacritty".
func Terminal() string {
	if t := os.Getenv("SWARMFORGE_TERMINAL"); t != "" {
		return t
	}
	return "alacritty"
}

// OpenWindows opens one terminal window per session, each attached to that tmux
// session on the given socket. Windows are spawned detached so the caller
// returns immediately; ending the tmux session (e.g. `swarmforge down`) ends the
// attach and closes the window. It returns the first error encountered but still
// attempts every window.
func OpenWindows(out io.Writer, socket string, windows []Window) error {
	term := Terminal()
	if _, err := exec.LookPath(term); err != nil {
		return fmt.Errorf("terminal %q not found on PATH (set SWARMFORGE_TERMINAL): %w", term, err)
	}
	var firstErr error
	for _, w := range windows {
		if err := openOne(term, socket, w); err != nil {
			fmt.Fprintf(out, "  failed to open window for %s: %v\n", w.Session, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		fmt.Fprintf(out, "  opened %s window -> %s\n", term, w.Session)
	}
	return firstErr
}

// attachArgs builds the terminal arguments to attach to a tmux session.
// Alacritty's flags are used by name; any other terminal falls back to the
// common `-e <command>` convention.
func attachArgs(term, socket string, w Window) []string {
	tmuxAttach := []string{"tmux", "-S", socket, "attach", "-t", w.Session}
	switch term {
	case "alacritty":
		return append([]string{"--title", w.Title, "--class", "swarmforge," + w.Session, "-e"}, tmuxAttach...)
	default:
		return append([]string{"-e"}, tmuxAttach...)
	}
}

func openOne(term, socket string, w Window) error {
	cmd := exec.Command(term, attachArgs(term, socket, w)...)
	// Detach fully: new session id, no controlling terminal, no stdio.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
