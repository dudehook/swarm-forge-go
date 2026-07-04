// Package orchestrator implements `swarmforge up` and `swarmforge down`: the
// full launch sequence (git init, worktrees, tmux sessions, PATH shims, handoff
// daemon, agent launch, attach) and teardown. It ports run-main! and the
// cleanup path of swarmforge.bb for the Linux/zsh/tmux-only target.
package orchestrator

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dudehook/swarmforge/internal/config"
	"github.com/dudehook/swarmforge/internal/daemon"
	"github.com/dudehook/swarmforge/internal/handoff"
	"github.com/dudehook/swarmforge/internal/launch"
	"github.com/dudehook/swarmforge/internal/project"
	"github.com/dudehook/swarmforge/internal/terminal"
	"github.com/dudehook/swarmforge/internal/tools"
)

const agentWindow = "swarm"

// shimCommands maps the PATH script name agents invoke to the binary subcommand
// it should exec. These keep the original command names available on PATH.
var shimCommands = map[string]string{
	"swarm_handoff.sh":           "send",
	"ready_for_next.sh":          "ready-for-next",
	"ready_for_next_task.sh":     "ready-for-next-task",
	"ready_for_next_batch.sh":    "ready-for-next-batch",
	"done_with_current.sh":       "done-with-current",
	"done_with_current_task.sh":  "done-with-current-task",
	"done_with_current_batch.sh": "done-with-current-batch",
	"stop_handoff_daemon.sh":     "stop-daemon",
}

// Options controls launch behavior.
type Options struct {
	// Attach controls whether Up attaches the current terminal to the first
	// session at the end. Disable for headless/scripted launches.
	Attach bool
	// DryRun parses and validates the config and prints the launch plan without
	// creating sessions, worktrees, or starting agents.
	DryRun bool
	// Windows opens one native terminal window per agent (instead of attaching
	// the current terminal to the first session).
	Windows bool
}

// Up runs the full launch sequence for the project at workDir.
func Up(out io.Writer, workDir string, opts Options) error {
	c, err := config.NewContext(workDir)
	if err != nil {
		return err
	}
	if opts.DryRun {
		if err := c.ParseConfig(); err != nil {
			return err
		}
		fmt.Fprintf(out, "Config OK: %s\n", c.ConfigFile)
		fmt.Fprintf(out, "tmux socket: %s\n", c.TmuxSocket)
		fmt.Fprintf(out, "%d role(s):\n", len(c.Roles))
		for i, r := range c.Roles {
			extra := ""
			if r.Provider != "" {
				extra += fmt.Sprintf("  provider=%s model=%s", r.Provider, r.Model)
			}
			if r.ExtraArgs != "" {
				extra += "  args=" + r.ExtraArgs
			}
			fmt.Fprintf(out, "  %d. %-10s agent=%-8s worktree=%-8s mode=%-5s session=%s%s\n",
				i+1, r.Name, r.Agent, r.WorktreeName, r.ReceiveMode, r.Session, extra)
		}
		warnMissingBackends(out, c)
		return nil
	}
	if err := checkDependency("tmux"); err != nil {
		return err
	}
	if err := checkDependency("git"); err != nil {
		return err
	}
	launch.DetectTmuxBaseIndexes(c)

	if err := initializeGitRepo(c); err != nil {
		return err
	}
	if err := ensureRuntimeGitExcludes(c); err != nil {
		return err
	}
	if err := c.ParseConfig(); err != nil {
		return err
	}
	if err := checkBackendDependencies(c); err != nil {
		return err
	}
	if err := c.PrepareWorkspace(); err != nil {
		return err
	}
	if err := prepareWorktrees(c); err != nil {
		return err
	}
	if err := prepareHandoffDirs(c); err != nil {
		return err
	}

	daemon.Stop(c.WorkingDir)
	killExistingSessions(c)

	fmt.Fprintln(out, "  SwarmForge (Go) starting")
	fmt.Fprintln(out, "Launching SwarmForge tmux sessions...")
	for _, r := range c.Roles {
		if err := createRoleSession(c, r.Session, r.DisplayName); err != nil {
			return err
		}
	}
	if err := writeTmuxEnvFile(c); err != nil {
		return err
	}
	if err := syncWorktreeScripts(c); err != nil {
		return err
	}
	if err := startHandoffDaemon(out, c); err != nil {
		return err
	}

	fmt.Fprintln(out, "Starting agents...")
	delay := time.Duration(config.AgentStartDelayMS()) * time.Millisecond
	for i, r := range c.Roles {
		if i > 0 {
			time.Sleep(delay)
		}
		if err := launchRole(out, c, i, r); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "SwarmForge is ready.")
	fmt.Fprintln(out, "Working directory:", c.WorkingDir)
	for _, r := range c.Roles {
		fmt.Fprintf(out, "  %s: %s\n", r.DisplayName, r.Session)
	}
	fmt.Fprintf(out, "Tip: reattach with 'tmux -S %s attach-session -t <session>'.\n", c.TmuxSocket)

	first := c.Roles[0].Session
	if opts.Windows {
		fmt.Fprintln(out, "Opening a terminal window per agent...")
		windows := make([]terminal.Window, len(c.Roles))
		for i, r := range c.Roles {
			windows[i] = terminal.Window{Title: "SwarmForge " + r.DisplayName, Session: r.Session}
		}
		if err := terminal.OpenWindows(out, c.TmuxSocket, windows); err != nil {
			return err
		}
		return nil
	}
	if !opts.Attach {
		fmt.Fprintf(out, "Swarm running (headless). Attach with: tmux -S %s attach-session -t %s\n", c.TmuxSocket, first)
		return nil
	}
	// Attach the current terminal to the first session (replaces the macOS/Windows
	// GUI surfaces, which are out of scope for the Linux/tmux-only port).
	fmt.Fprintf(out, "Attaching to '%s'...\n", first)
	attach := exec.Command("tmux", "-S", c.TmuxSocket, "attach-session", "-t", first)
	attach.Stdin, attach.Stdout, attach.Stderr = os.Stdin, os.Stdout, os.Stderr
	return attach.Run()
}

// Down tears the swarm down: stops the handoff daemon and kills all sessions.
// It derives everything from the working directory's project state, so it can be
// invoked with no arguments (including from the lead agent's teardown trailer).
func Down(out io.Writer, workDir string) error {
	root, err := project.Root(workDir)
	if err != nil {
		// Nothing to clean up if we cannot locate the project.
		return nil
	}
	daemon.Stop(root)

	socket := readTrimmed(filepath.Join(root, ".swarmforge", "tmux-socket"))
	for _, session := range sessionsFromFile(filepath.Join(root, ".swarmforge", "sessions.tsv")) {
		if socket != "" {
			exec.Command("tmux", "-S", socket, "kill-session", "-t", session).Run()
		}
		fmt.Fprintf(out, "Stopped session %s\n", session)
	}
	return nil
}

func initializeGitRepo(c *config.Context) error {
	if dirExists(filepath.Join(c.WorkingDir, ".git")) {
		return nil
	}
	steps := [][]string{
		{"init", c.WorkingDir},
		{"-C", c.WorkingDir, "branch", "-M", "master"},
	}
	for _, args := range steps {
		if err := exec.Command("git", args...).Run(); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	if err := ensureInitialGitignore(c); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"-C", c.WorkingDir, "add", "."},
		{"-C", c.WorkingDir, "commit", "-m", "Initial swarmforge repository"},
	} {
		if err := exec.Command("git", args...).Run(); err != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
	}
	return nil
}

func ensureInitialGitignore(c *config.Context) error {
	gitignore := filepath.Join(c.WorkingDir, ".gitignore")
	if !fileExists(gitignore) {
		return os.WriteFile(gitignore, []byte(".swarmforge/\n.worktrees/\n"), 0o644)
	}
	if err := ensureLine(gitignore, ".swarmforge/"); err != nil {
		return err
	}
	return ensureLine(gitignore, ".worktrees/")
}

func ensureRuntimeGitExcludes(c *config.Context) error {
	out, err := exec.Command("git", "-C", c.WorkingDir, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return err
	}
	excludeFile := strings.TrimSpace(string(out))
	if !filepath.IsAbs(excludeFile) {
		excludeFile = filepath.Join(c.WorkingDir, excludeFile)
	}
	if err := os.MkdirAll(filepath.Dir(excludeFile), 0o755); err != nil {
		return err
	}
	if err := ensureLine(excludeFile, ".swarmforge/"); err != nil {
		return err
	}
	return ensureLine(excludeFile, ".worktrees/")
}

// warnMissingBackends reports (without failing) any agent backend that is not on
// PATH. Used by --dry-run so config validation still succeeds on a machine that
// lacks a harness the config references (e.g. opencode not installed yet).
func warnMissingBackends(out io.Writer, c *config.Context) {
	seen := map[string]bool{}
	for _, r := range c.Roles {
		if seen[r.Agent] {
			continue
		}
		seen[r.Agent] = true
		if _, err := exec.LookPath(r.Agent); err != nil {
			fmt.Fprintf(out, "Warning: backend '%s' is not installed but is required by the config.\n", r.Agent)
		}
	}
}

func checkBackendDependencies(c *config.Context) error {
	seen := map[string]bool{}
	for _, r := range c.Roles {
		if seen[r.Agent] {
			continue
		}
		seen[r.Agent] = true
		if err := checkDependency(r.Agent); err != nil {
			return err
		}
	}
	return nil
}

func prepareWorktrees(c *config.Context) error {
	for _, r := range c.Roles {
		if r.WorktreeName == "master" || r.WorktreeName == "none" {
			continue
		}
		if dirExists(filepath.Join(r.WorktreePath, ".git")) || fileExists(filepath.Join(r.WorktreePath, ".git")) {
			continue
		}
		branch := "swarmforge-" + r.WorktreeName
		if err := exec.Command("git", "-C", c.WorkingDir, "worktree", "add", "--force", "-B", branch, r.WorktreePath, "HEAD").Run(); err != nil {
			return fmt.Errorf("git worktree add %s: %w", r.WorktreeName, err)
		}
	}
	return nil
}

func prepareHandoffDirs(c *config.Context) error {
	subdirs := []string{"outbox/tmp", "sent", "failed", "inbox/new", "inbox/in_process", "inbox/completed"}
	for _, r := range c.Roles {
		for _, sub := range subdirs {
			if err := os.MkdirAll(filepath.Join(r.WorktreePath, ".swarmforge", "handoffs", sub), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

func killExistingSessions(c *config.Context) {
	for _, r := range c.Roles {
		if exec.Command("tmux", "-S", c.TmuxSocket, "has-session", "-t", r.Session).Run() == nil {
			exec.Command("tmux", "-S", c.TmuxSocket, "kill-session", "-t", r.Session).Run()
		}
	}
}

func createRoleSession(c *config.Context, session, title string) error {
	if err := exec.Command("tmux", "-S", c.TmuxSocket, "new-session", "-d", "-s", session, "-n", agentWindow).Run(); err != nil {
		return fmt.Errorf("tmux new-session %s: %w", session, err)
	}
	exec.Command("tmux", "-S", c.TmuxSocket, "rename-window", "-t", session+":"+agentWindow, title).Run()
	exec.Command("tmux", "-S", c.TmuxSocket, "set-window-option", "-t", session+":"+title, "allow-rename", "off").Run()
	return nil
}

func writeTmuxEnvFile(c *config.Context) error {
	out, err := exec.Command("tmux", "-S", c.TmuxSocket, "display-message", "-p", "#{socket_path},#{pid},#{pane_id}").Output()
	if err != nil {
		return err
	}
	return os.WriteFile(c.TmuxEnvFile, []byte(strings.TrimSpace(string(out))+"\n"), 0o644)
}

// syncWorktreeScripts writes the PATH shims into every role's scripts directory
// and copies the shared state files into non-master worktrees.
func syncWorktreeScripts(c *config.Context) error {
	if err := writeShims(c, c.ScriptDir); err != nil {
		return err
	}
	// The master working dir's PATH is shared by every role that runs there, so
	// install the union of their harnesses' needed fallbacks.
	if err := provisionTools(c.ToolsDir, masterHarnesses(c)); err != nil {
		return err
	}
	for _, r := range c.Roles {
		if r.WorktreePath == c.WorkingDir {
			continue
		}
		scriptsDir := filepath.Join(r.WorktreePath, "swarmforge", "scripts")
		if err := writeShims(c, scriptsDir); err != nil {
			return err
		}
		toolsDir := filepath.Join(r.WorktreePath, "swarmforge", "tools")
		if err := provisionTools(toolsDir, []string{r.Agent}); err != nil {
			return err
		}
		roleState := filepath.Join(r.WorktreePath, ".swarmforge")
		if err := os.MkdirAll(filepath.Join(roleState, "notify"), 0o755); err != nil {
			return err
		}
		for name, src := range map[string]string{
			"sessions.tsv": c.SessionsFile,
			"roles.tsv":    c.RolesFile,
			"tmux-socket":  c.TmuxSocketFile,
			"tmux-env":     c.TmuxEnvFile,
		} {
			if err := copyFile(src, filepath.Join(roleState, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// masterHarnesses returns the harness of every role that runs in the master
// working directory. These roles share c.ToolsDir on one PATH, so their tool
// fallbacks are provisioned as a union.
func masterHarnesses(c *config.Context) []string {
	var hs []string
	for _, r := range c.Roles {
		if r.WorktreePath == c.WorkingDir {
			hs = append(hs, r.Agent)
		}
	}
	return hs
}

// provisionTools installs the fallback capability scripts the given harnesses
// need into dir (those not provided natively by every sharing harness) and
// writes the generated tools manifest the agent reads. It is harness-blind from
// the agent's side: the agent only ever sees the resulting commands on its PATH.
func provisionTools(dir string, harnesses []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	caps := tools.FallbacksForAll(harnesses)
	for _, capa := range caps {
		if err := os.WriteFile(filepath.Join(dir, capa.Name), []byte(capa.Script), 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "README.md"), []byte(tools.Manifest(caps)), 0o644)
}

func writeShims(c *config.Context, dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, sub := range shimCommands {
		content := "#!/usr/bin/env zsh\nexec " + launch.ShellQuote(c.SelfExe) + " " + sub + " \"$@\"\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func startHandoffDaemon(out io.Writer, c *config.Context) error {
	os.Remove(filepath.Join(c.DaemonDir, "stop"))
	args := append(launch.SleepInhibitorPrefix(), c.SelfExe, "handoffd", c.WorkingDir)
	logFile, err := os.OpenFile(c.HandoffDaemonLog, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return err
	}
	go func() { cmd.Wait(); logFile.Close() }()
	fmt.Fprintln(out, "Started handoff daemon"+inhibitorNote()+".")
	return nil
}

func inhibitorNote() string {
	if len(launch.SleepInhibitorPrefix()) > 0 {
		return " with OS sleep prevention"
	}
	return ""
}

func launchRole(out io.Writer, c *config.Context, index int, r config.Role) error {
	command, err := launch.Command(c, index, r)
	if err != nil {
		return err
	}
	target := fmt.Sprintf("%s:%s.%d", r.Session, r.DisplayName, c.PaneBaseIndex)
	if err := exec.Command("tmux", "-S", c.TmuxSocket, "send-keys", "-t", target, command, "Enter").Run(); err != nil {
		return fmt.Errorf("tmux send-keys %s: %w", r.Session, err)
	}
	fmt.Fprintf(out, "  [%s] started in session %s\n", r.DisplayName, r.Session)
	return nil
}

func checkDependency(command string) error {
	if _, err := exec.LookPath(command); err != nil {
		return &handoff.ExitError{Code: 1, Message: "Error: '" + command + "' is required but not installed."}
	}
	return nil
}

func ensureLine(file, line string) error {
	data, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, existing := range strings.Split(string(data), "\n") {
		if existing == line {
			return nil
		}
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func sessionsFromFile(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var sessions []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) >= 3 {
			sessions = append(sessions, fields[2])
		}
	}
	return sessions
}

func readTrimmed(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
