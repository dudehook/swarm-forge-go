// Command swarmforge is the single-binary SwarmForge orchestrator.
//
// This first slice implements the handoff subsystem subcommands. The agent-facing
// commands keep names that mirror the original scripts so PATH shims can map to
// them directly.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	root "github.com/dudehook/swarmforge"
	"github.com/dudehook/swarmforge/internal/config"
	"github.com/dudehook/swarmforge/internal/daemon"
	"github.com/dudehook/swarmforge/internal/handoff"
	"github.com/dudehook/swarmforge/internal/inbox"
	"github.com/dudehook/swarmforge/internal/orchestrator"
	"github.com/dudehook/swarmforge/internal/project"
	"github.com/dudehook/swarmforge/internal/scaffold"
	"github.com/dudehook/swarmforge/internal/send"
	"github.com/dudehook/swarmforge/internal/terminal"
	"github.com/dudehook/swarmforge/internal/tui"
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
	case "init":
		return runInit(args)
	case "template", "templates", "list-templates":
		return runTemplates(args)
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
	case "tui", "dashboard":
		return runTui()
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
	var wantTUI bool
	for _, a := range args {
		switch a {
		case "--no-attach":
			opts.Attach = false
		case "--dry-run", "-n":
			opts.DryRun = true
		case "--windows", "-w":
			opts.Windows = true
		case "--tui":
			wantTUI = true
			opts.Attach = false
		}
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := orchestrator.Up(os.Stdout, workDir, opts); err != nil {
		return err
	}
	if wantTUI && !opts.DryRun {
		return tui.Run(workDir)
	}
	return nil
}

// runTui opens the read-only dashboard for the swarm in the current directory.
func runTui() error {
	if !stdinIsTerminal() {
		return &handoff.ExitError{Code: 1, Message: "swarmforge tui requires an interactive terminal"}
	}
	workDir, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err := project.Root(workDir)
	if err != nil {
		return &handoff.ExitError{Code: 1, Message: "No SwarmForge project here (run 'swarmforge up' first)."}
	}
	return tui.Run(root)
}

// runInit scaffolds the current (or a new) directory into a SwarmForge project
// from a template.
func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	var opts scaffold.Options
	var listOnly bool
	fs.StringVar(&opts.TemplateName, "template", "", "template name (see 'swarmforge templates')")
	fs.StringVar(&opts.TemplateName, "t", "", "template name (shorthand)")
	fs.StringVar(&opts.Harness, "harness", "", "harness (agent CLI) for all roles (default: template's, else claude)")
	fs.StringVar(&opts.TargetDir, "dir", ".", "target project directory")
	fs.StringVar(&opts.TemplatesDir, "templates-dir", "", "override templates directory")
	fs.BoolVar(&opts.New, "new", false, "create the target directory if it does not exist")
	fs.BoolVar(&opts.Yolo, "yolo", false, "add --yolo (auto-approve) to every role")
	fs.BoolVar(&opts.Force, "force", false, "overwrite an existing swarmforge/ directory")
	fs.BoolVar(&listOnly, "list-templates", false, "list available templates and exit")
	var noEdit bool
	fs.BoolVar(&noEdit, "no-edit", false, "with --new, do not open $EDITOR on project.prompt afterward")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if listOnly {
		return listTemplates(opts.TemplatesDir)
	}
	if err := scaffold.Init(os.Stdout, opts); err != nil {
		return err
	}
	// For a freshly created project, drop the user into the project article so
	// they can fill in their task. (A child process can't cd the parent shell,
	// so we open the editor in the project dir and print the cd next-step.)
	if opts.New {
		if target, err := filepath.Abs(opts.TargetDir); err == nil {
			openProjectArticle(target, noEdit)
		}
	}
	return nil
}

// openProjectArticle opens $EDITOR (default vi) on the project's project.prompt,
// with the editor's working directory set to the project. Skipped when noEdit is
// set, when the file is absent, or when stdin is not a terminal (so it never
// blocks in scripts/CI).
func openProjectArticle(projectDir string, noEdit bool) {
	promptPath := filepath.Join(projectDir, "swarmforge", "constitution", "articles", "project.prompt")
	if _, err := os.Stat(promptPath); err != nil {
		return
	}
	nextStep := fmt.Sprintf("Next: cd %s && swarmforge up", projectDir)
	if noEdit || !stdinIsTerminal() {
		fmt.Printf("Edit %s to describe your task.\n%s\n", promptPath, nextStep)
		return
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	fields := strings.Fields(editor)
	cmd := exec.Command(fields[0], append(fields[1:], promptPath)...)
	cmd.Dir = projectDir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "could not open editor %q: %v\n", editor, err)
	}
	fmt.Println(nextStep)
}

// stdinIsTerminal reports whether stdin is a real terminal (not a pipe, file, or
// /dev/null). It uses the TCGETS ioctl, which only succeeds on a tty — a plain
// character-device check is not enough (/dev/null is a character device too).
func stdinIsTerminal() bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&termios)))
	return errno == 0
}

// runTemplates lists available templates, or installs the embedded templates
// into the user templates directory (`templates install` / `template install`).
func runTemplates(args []string) error {
	if len(args) > 0 && args[0] == "install" {
		return runTemplateInstall(args[1:])
	}
	fs := flag.NewFlagSet("templates", flag.ContinueOnError)
	dir := fs.String("templates-dir", "", "override templates directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return listTemplates(*dir)
}

// runTemplateInstall copies the binary's embedded templates into the user
// templates directory so `init`/`templates` can find them.
func runTemplateInstall(args []string) error {
	fs := flag.NewFlagSet("template install", flag.ContinueOnError)
	dir := fs.String("templates-dir", "", "override templates directory")
	force := fs.Bool("force", false, "overwrite templates that are already installed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return scaffold.InstallTemplates(os.Stdout, root.TemplatesFS(), *dir, *force)
}

func listTemplates(override string) error {
	dir := scaffold.TemplatesDir(override)
	templates, err := scaffold.List(dir)
	if err != nil {
		return err
	}
	if len(templates) == 0 {
		fmt.Printf("No templates found in %s\n", dir)
		fmt.Println("Install a template there (or set SWARMFORGE_TEMPLATES_DIR / --templates-dir).")
		return nil
	}
	fmt.Printf("Templates in %s:\n", dir)
	for _, t := range templates {
		fmt.Printf("  %-16s %s\n", t.Name, t.Description)
	}
	return nil
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
	fmt.Fprintln(os.Stderr, "Setup commands:")
	fmt.Fprintln(os.Stderr, "  init [-t NAME] [--harness H] [--new] [--dir D] [--yolo]")
	fmt.Fprintln(os.Stderr, "                            scaffold a project into a SwarmForge project from a template")
	fmt.Fprintln(os.Stderr, "  templates                 list available templates")
	fmt.Fprintln(os.Stderr, "  templates install [--force] [--templates-dir D]")
	fmt.Fprintln(os.Stderr, "                            install the built-in templates into the user templates dir")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Swarm commands:")
	fmt.Fprintln(os.Stderr, "  up [--windows|--tui|--no-attach|--dry-run]")
	fmt.Fprintln(os.Stderr, "                            launch the swarm from swarmforge/swarmforge.conf")
	fmt.Fprintln(os.Stderr, "  down                      stop the daemon and kill all sessions")
	fmt.Fprintln(os.Stderr, "  windows                   open a terminal window per agent (swarm must be running)")
	fmt.Fprintln(os.Stderr, "  tui                       open the read-only dashboard for a running swarm")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Handoff commands:")
	fmt.Fprintln(os.Stderr, "  handoff send <draft>      validate and queue a handoff draft")
	fmt.Fprintln(os.Stderr, "  ready-for-next            accept/resume the next task or batch for this role")
	fmt.Fprintln(os.Stderr, "  done-with-current         complete current work, then accept the next")
	fmt.Fprintln(os.Stderr, "  handoffd <root>           run the handoff delivery daemon")
}
