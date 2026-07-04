// Package launch builds the shell command that starts an agent in its tmux
// pane, plus the Linux sleep inhibitor and tmux base-index detection. It ports
// launch-command and the related helpers in swarmforge.bb.
package launch

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dudehook/swarmforge/internal/config"
)

var bypassRe = regexp.MustCompile(`--permission-mode\s+bypassPermissions`)

// ShellQuote single-quotes a value for safe use in a shell command, escaping
// embedded single quotes. Ports the sq helper.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// WriteAgentInstructionFile writes the per-role prompt bootstrap file the agent
// reads on startup.
func WriteAgentInstructionFile(role, promptFile string) error {
	if err := os.MkdirAll(filepath.Dir(promptFile), 0o755); err != nil {
		return err
	}
	content := "Read swarmforge/constitution.prompt, then read every file it refers to recursively, and obey all of those instructions.\n" +
		"Read swarmforge/roles/" + role + ".prompt, then read every file it refers to recursively, and follow all of those instructions.\n"
	return os.WriteFile(promptFile, []byte(content), 0o644)
}

func extraArgsPrefix(r config.Role) string {
	if strings.TrimSpace(r.ExtraArgs) == "" {
		return ""
	}
	return r.ExtraArgs + " "
}

// modelPrefix returns the `--model <model> ` flag when the role resolved through
// a provider that pins a model, or "" for the harness's default model. Used by
// the claude/codex/grok harnesses, which all accept --model.
func modelPrefix(r config.Role) string {
	if strings.TrimSpace(r.Model) == "" {
		return ""
	}
	return "--model " + ShellQuote(r.Model) + " "
}

// wantsAutoApprove reports whether the role requested full unattended
// auto-approval via a --yolo / --always-approve marker (or an explicit
// bypassPermissions mode) in its extra CLI args.
func wantsAutoApprove(r config.Role) bool {
	args := r.ExtraArgs
	return strings.Contains(args, "--always-approve") ||
		strings.Contains(args, "--yolo") ||
		bypassRe.MatchString(args)
}

// permissionPrefix returns the Claude/grok --permission-mode flag: bypass when
// auto-approval is requested, acceptEdits (edits only) otherwise.
func permissionPrefix(r config.Role) string {
	// acceptEdits only auto-approves file edits; bypassPermissions is the
	// CLI-enforced mode that also auto-approves shell commands (tests, git,
	// the handoff scripts) — needed for an unattended swarm.
	if wantsAutoApprove(r) {
		return "--permission-mode bypassPermissions "
	}
	return "--permission-mode acceptEdits "
}

// nonMarkerExtraArgsPrefix passes through the role's extra args minus the
// SwarmForge auto-approve markers (--yolo/--always-approve), which the claude
// and opencode CLIs do not understand — they use their own permission flags.
func nonMarkerExtraArgsPrefix(r config.Role) string {
	var kept []string
	for _, tok := range strings.Fields(r.ExtraArgs) {
		if tok == "--yolo" || tok == "--always-approve" {
			continue
		}
		kept = append(kept, tok)
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, " ") + " "
}

// Command builds the full shell command line for launching the agent for row.
// When index == 0 it appends the teardown trailer that runs `swarmforge down`
// after the lead agent exits (replacing the original swarm-cleanup hook).
func Command(c *config.Context, index int, row config.Role) (string, error) {
	roleWorktree := row.WorktreePath
	roleScriptDir := c.ScriptDir
	roleToolsDir := c.ToolsDir
	if roleWorktree != c.WorkingDir {
		roleScriptDir = filepath.Join(roleWorktree, "swarmforge", "scripts")
		roleToolsDir = filepath.Join(roleWorktree, "swarmforge", "tools")
	}
	promptFile := filepath.Join(c.PromptsDir, row.Name+".md")

	base := "export SWARMFORGE_ROLE=" + ShellQuote(row.Name) +
		" && export PATH=" + ShellQuote(roleScriptDir) + ":" + ShellQuote(roleToolsDir) + ":$PATH" +
		" && cd " + ShellQuote(roleWorktree) +
		" && "
	if row.Agent == "opencode" {
		// opencode discovers the generated provider (baseURL/model) via this env.
		base = "export OPENCODE_CONFIG=" + ShellQuote(c.OpenCodeConfig) + " && " + base
	}

	if err := WriteAgentInstructionFile(row.Name, promptFile); err != nil {
		return "", err
	}

	q := ShellQuote(promptFile)
	display := ShellQuote("SwarmForge " + row.DisplayName)
	var agentCmd string
	switch row.Agent {
	case "claude":
		agentCmd = "claude --append-system-prompt-file " + q + " " + permissionPrefix(row) + modelPrefix(row) + "-n " + display + " " + nonMarkerExtraArgsPrefix(row) + `"$(cat ` + q + `)"`
	case "codex":
		agentCmd = "codex -C " + ShellQuote(roleWorktree) + " " + modelPrefix(row) + extraArgsPrefix(row) + `"$(cat ` + q + `)"`
	case "copilot":
		agentCmd = "copilot -C " + ShellQuote(roleWorktree) + " --name " + display + " " + extraArgsPrefix(row) + `-i "$(cat ` + q + `)"`
	case "grok":
		agentCmd = "grok --cwd " + ShellQuote(roleWorktree) + " " + permissionPrefix(row) + modelPrefix(row) + extraArgsPrefix(row) + `--rules "$(cat ` + q + `)" --verbatim "$(cat ` + q + `)"`
	case "opencode":
		// opencode registers the endpoint under the provider name, referenced as
		// <provider>/<model>. --auto auto-approves permissions for unattended runs.
		autoFlag := ""
		if wantsAutoApprove(row) {
			autoFlag = "--auto "
		}
		modelRef := ShellQuote(row.Provider + "/" + row.Model)
		agentCmd = "opencode --dir " + ShellQuote(roleWorktree) + " --model " + modelRef + " " + autoFlag + nonMarkerExtraArgsPrefix(row) + `--prompt "$(cat ` + q + `)"`
	}

	cmd := base + agentCmd
	if index == 0 {
		cmd += teardownTrailer(c)
	}
	return cmd, nil
}

// teardownTrailer runs `swarmforge down` in the background after the lead
// agent's command exits, then preserves the agent's exit code.
func teardownTrailer(c *config.Context) string {
	sessions := make([]string, len(c.Roles))
	for i, r := range c.Roles {
		sessions[i] = " " + ShellQuote(r.Session)
	}
	return "; exit_code=$?; nohup " + ShellQuote(c.SelfExe) + " down " + ShellQuote(c.TmuxSocket) +
		strings.Join(sessions, "") + " >/dev/null 2>&1 &!; exit $exit_code"
}

// SleepInhibitorPrefix returns the command prefix that prevents the host from
// sleeping while the swarm runs, or nil when disabled or unavailable. Linux only.
func SleepInhibitorPrefix() []string {
	if os.Getenv("SWARMFORGE_PREVENT_SLEEP") == "0" {
		return nil
	}
	if commandExists("systemd-inhibit") && commandExists("systemctl") && linuxSystemdRunning() {
		return []string{
			"systemd-inhibit",
			"--what=sleep:idle",
			"--who=SwarmForge",
			"--why=SwarmForge swarm is active",
		}
	}
	return nil
}

func linuxSystemdRunning() bool {
	out, _ := exec.Command("systemctl", "is-system-running").Output()
	state := strings.TrimSpace(string(out))
	return state == "running" || state == "degraded"
}

// DetectTmuxBaseIndexes queries the tmux server (starting a short-lived probe
// session if needed) for the global base-index and pane-base-index, defaulting
// to 0 each. It ports detect-tmux-base-indexes.
func DetectTmuxBaseIndexes(c *config.Context) {
	os.MkdirAll(c.TmuxSocketDir, 0o755)
	var probe string
	if exec.Command("tmux", "-S", c.TmuxSocket, "info").Run() != nil {
		probe = "swarmforge-probe-" + strconv.Itoa(os.Getpid())
		exec.Command("tmux", "-S", c.TmuxSocket, "new-session", "-d", "-s", probe, "sleep 60").Run()
	}
	c.WindowBaseIndex = tmuxOption(c.TmuxSocket, "base-index", false, 0)
	c.PaneBaseIndex = tmuxOption(c.TmuxSocket, "pane-base-index", true, 0)
	if probe != "" {
		exec.Command("tmux", "-S", c.TmuxSocket, "kill-session", "-t", probe).Run()
	}
}

func tmuxOption(socket, option string, windowScope bool, def int) int {
	scope := "-gqv"
	if windowScope {
		scope = "-gwqv"
	}
	out, err := exec.Command("tmux", "-S", socket, "show-options", scope, option).Output()
	if err != nil {
		return def
	}
	value := strings.TrimSpace(string(out))
	if n, err := strconv.Atoi(value); err == nil && n >= 0 {
		return n
	}
	return def
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
