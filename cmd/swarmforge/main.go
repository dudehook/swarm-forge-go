// Command swarmforge is the single-binary SwarmForge orchestrator.
//
// This first slice implements the handoff subsystem subcommands. The agent-facing
// commands keep names that mirror the original scripts so PATH shims can map to
// them directly.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dudehook/swarmforge/internal/config"
	"github.com/dudehook/swarmforge/internal/daemon"
	"github.com/dudehook/swarmforge/internal/handoff"
	"github.com/dudehook/swarmforge/internal/inbox"
	"github.com/dudehook/swarmforge/internal/orchestrator"
	"github.com/dudehook/swarmforge/internal/project"
	"github.com/dudehook/swarmforge/internal/send"
	"github.com/dudehook/swarmforge/internal/terminal"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := dispatch(os.Args[1], os.Args[2:]); err != nil {
		var exitErr *handoff.ExitError
		if errors.As(err, &exitErr) {
			if exitErr.Message != "" {
				fmt.Fprintln(os.Stderr, exitErr.Message)
			}
			os.Exit(exitErr.Code)
		}
		var usageErr send.UsageError
		if errors.As(err, &usageErr) {
			fmt.Fprintln(os.Stderr, usageErr.Error())
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func dispatch(cmd string, args []string) error {
	switch cmd {
	case "handoff", "send", "swarm_handoff":
		return runSend(args)
	case "ready-for-next", "ready_for_next":
		return runInbox(inbox.ReadyForNext)
	case "ready-for-next-task", "ready_for_next_task":
		return runInboxDirect(inbox.ReadyForNextTask)
	case "ready-for-next-batch", "ready_for_next_batch":
		return runInboxDirect(inbox.ReadyForNextBatch)
	case "done-with-current", "done_with_current":
		return runInbox(inbox.DoneWithCurrent)
	case "done-with-current-task", "done_with_current_task":
		return runInboxDirect(inbox.DoneWithCurrentTask)
	case "done-with-current-batch", "done_with_current_batch":
		return runInboxDirect(inbox.DoneWithCurrentBatch)
	case "up":
		return runUp(args)
	case "down":
		return runInWorkDir(orchestrator.Down)
	case "windows", "open-windows":
		return runWindows()
	case "handoffd":
		return runDaemon(args)
	case "stop-daemon", "stop_handoff_daemon":
		return runStopDaemon(args)
	default:
		usage()
		return &handoff.ExitError{Code: 2, Message: "Unknown command: " + cmd}
	}
}

// runSend handles the "handoff send" subcommand. The draft path is the final
// argument (so both "swarmforge handoff send <draft>" and "swarmforge send
// <draft>" work).
func runSend(args []string) error {
	if len(args) > 0 && args[0] == "send" {
		args = args[1:]
	}
	if len(args) != 1 {
		return send.UsageError{}
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := project.Root(workDir)
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: err.Error()}
	}
	sender, err := project.Role()
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: err.Error()}
	}
	return send.Send(os.Stdout, workDir, root, sender, args[0])
}

// runInbox handles the mode-dispatching inbox commands (ready-for-next,
// done-with-current), which need the role and project root.
func runInbox(fn func(out io.Writer, workDir, root, role string) error) error {
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := project.Root(workDir)
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: err.Error()}
	}
	role, err := project.Role()
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: err.Error()}
	}
	return fn(os.Stdout, workDir, root, role)
}

// runInboxDirect handles the task/batch-specific inbox commands, which operate
// purely on the working directory's inbox.
func runInboxDirect(fn func(out io.Writer, workDir string) error) error {
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	return fn(os.Stdout, workDir)
}

// runUp launches the swarm. With --no-attach it returns once agents are started
// instead of attaching the terminal to the first session.
func runUp(args []string) error {
	opts := orchestrator.Options{Attach: true}
	for _, a := range args {
		switch a {
		case "--no-attach":
			opts.Attach = false
		case "--dry-run", "-n":
			opts.DryRun = true
		case "--windows", "-w":
			opts.Windows = true
		}
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	return orchestrator.Up(os.Stdout, workDir, opts)
}

// runWindows opens one terminal window per agent, each attached to its tmux
// session. Requires a running swarm (sessions.tsv + tmux-socket present).
func runWindows() error {
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := project.Root(workDir)
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: err.Error()}
	}
	socketBytes, err := os.ReadFile(filepath.Join(root, ".swarmforge", "tmux-socket"))
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: "No running swarm found here (missing .swarmforge/tmux-socket). Run 'swarmforge up' first."}
	}
	socket := strings.TrimSpace(string(socketBytes))
	rows, err := config.ReadSessions(root)
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: "Could not read sessions: " + err.Error()}
	}
	windows := make([]terminal.Window, len(rows))
	for i, r := range rows {
		windows[i] = terminal.Window{Title: "SwarmForge " + r.Display, Session: r.Session}
	}
	return terminal.OpenWindows(os.Stdout, socket, windows)
}

// runInWorkDir runs an orchestrator command (down) against the current
// working directory.
func runInWorkDir(fn func(out io.Writer, workDir string) error) error {
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	return fn(os.Stdout, workDir)
}

// runDaemon runs the handoff delivery daemon for the given project root
// (defaulting to the working directory).
func runDaemon(args []string) error {
	root := ""
	if len(args) > 0 {
		root = args[0]
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		root = wd
	}
	return daemon.New(root).Run()
}

// runStopDaemon stops the handoff daemon for the given project root.
func runStopDaemon(args []string) error {
	root := ""
	if len(args) > 0 {
		root = args[0]
	}
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		root = wd
	}
	return daemon.Stop(root)
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: swarmforge <command> [args...]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Swarm commands:")
	fmt.Fprintln(os.Stderr, "  up [--windows|--no-attach|--dry-run]")
	fmt.Fprintln(os.Stderr, "                            launch the swarm from swarmforge/swarmforge.conf")
	fmt.Fprintln(os.Stderr, "  down                      stop the daemon and kill all sessions")
	fmt.Fprintln(os.Stderr, "  windows                   open a terminal window per agent (swarm must be running)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Handoff commands:")
	fmt.Fprintln(os.Stderr, "  handoff send <draft>      validate and queue a handoff draft")
	fmt.Fprintln(os.Stderr, "  ready-for-next            accept/resume the next task or batch for this role")
	fmt.Fprintln(os.Stderr, "  done-with-current         complete current work, then accept the next")
	fmt.Fprintln(os.Stderr, "  handoffd <root>           run the handoff delivery daemon")
}
