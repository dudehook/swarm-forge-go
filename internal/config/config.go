// Package config parses swarmforge.conf into role definitions and computes the
// per-project context (paths and the tmux socket). It ports the configuration
// and context portions of swarmforge.bb.
//
// macOS/Windows terminal-backend fields and the GUI window-tracking files are
// intentionally omitted: this port targets Linux + zsh + tmux only.
package config

import (
	"fmt"
	"hash/crc32"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dudehook/swarmforge/internal/handoff"
)

var (
	supportedAgents = map[string]bool{"claude": true, "codex": true, "copilot": true, "grok": true}
	receiveModes    = map[string]bool{"task": true, "batch": true}
	wordSplit       = regexp.MustCompile(`\s+`)
	digits          = regexp.MustCompile(`^[0-9]+$`)
)

// Role is one configured agent window.
type Role struct {
	Name         string
	Agent        string
	Session      string
	DisplayName  string
	WorktreeName string
	WorktreePath string
	ReceiveMode  string
	ExtraArgs    string
}

// Context holds resolved paths and state for a project working directory.
type Context struct {
	WorkingDir       string
	ScriptDir        string // directory of shim commands placed on each agent's PATH
	SwarmForgeDir    string
	WorktreesDir     string
	ConfigFile       string
	RolesDir         string
	ConstitutionFile string
	StateDir         string
	NotifyDir        string
	SessionsFile     string
	RolesFile        string
	PromptsDir       string
	DaemonDir        string
	HandoffDaemonLog string
	TmuxSocketDir    string
	TmuxSocket       string
	TmuxSocketFile   string
	TmuxEnvFile      string
	SelfExe          string

	WindowBaseIndex int
	PaneBaseIndex   int

	Roles []Role
}

// NewContext builds the context for workingDir, including the portable tmux
// socket path derived from a CRC32 of the absolute working directory.
func NewContext(workingDir string) (*Context, error) {
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return nil, err
	}
	swarmDir := filepath.Join(abs, "swarmforge")
	stateDir := filepath.Join(abs, ".swarmforge")
	daemonDir := filepath.Join(stateDir, "daemon")

	socketID := strconv.FormatUint(uint64(crc32.ChecksumIEEE([]byte(abs))), 10)
	socketDir := filepath.Join("/tmp", "swarmforge-"+socketOwner())
	exe, _ := os.Executable()

	return &Context{
		WorkingDir:       abs,
		ScriptDir:        filepath.Join(swarmDir, "scripts"),
		SwarmForgeDir:    swarmDir,
		WorktreesDir:     filepath.Join(abs, ".worktrees"),
		ConfigFile:       filepath.Join(swarmDir, "swarmforge.conf"),
		RolesDir:         filepath.Join(swarmDir, "roles"),
		ConstitutionFile: filepath.Join(swarmDir, "constitution.prompt"),
		StateDir:         stateDir,
		NotifyDir:        filepath.Join(stateDir, "notify"),
		SessionsFile:     filepath.Join(stateDir, "sessions.tsv"),
		RolesFile:        filepath.Join(stateDir, "roles.tsv"),
		PromptsDir:       filepath.Join(stateDir, "prompts"),
		DaemonDir:        daemonDir,
		HandoffDaemonLog: filepath.Join(daemonDir, "handoffd.log"),
		TmuxSocketDir:    socketDir,
		TmuxSocket:       filepath.Join(socketDir, socketID+".sock"),
		TmuxSocketFile:   filepath.Join(stateDir, "tmux-socket"),
		TmuxEnvFile:      filepath.Join(stateDir, "tmux-env"),
		SelfExe:          exe,
	}, nil
}

func socketOwner() string {
	if uid := os.Getenv("UID"); uid != "" {
		return uid
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "unknown"
}

// ParseConfig reads and validates swarmforge.conf, populating c.Roles. It
// reproduces the validation order and messages of swarmforge.bb's parse-config.
func (c *Context) ParseConfig() error {
	if !exists(c.ConfigFile) {
		return failf("Config not found at %s", c.ConfigFile)
	}
	if !exists(c.ConstitutionFile) {
		return failf("Constitution prompt not found at %s", c.ConstitutionFile)
	}
	data, err := os.ReadFile(c.ConfigFile)
	if err != nil {
		return err
	}

	var rows []Role
	seenRoles := map[string]bool{}
	seenWorktrees := map[string]bool{}

	for i, raw := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := wordSplit.Split(line, -1)
		if len(fields) < 4 {
			return failf("Invalid config line %d: %s", lineNo, line)
		}
		keyword := fields[0]
		role := fields[1]
		agent := strings.ToLower(fields[2])
		worktree := fields[3]
		trailing := fields[4:]

		receiveMode := "task"
		extraTokens := trailing
		if len(trailing) > 0 && receiveModes[trailing[0]] {
			receiveMode = trailing[0]
			extraTokens = trailing[1:]
		}
		extraArgs := ""
		if len(extraTokens) > 0 {
			extraArgs = strings.Join(extraTokens, " ")
		}

		if keyword != "window" {
			return failf("Unknown config directive on line %d: %s", lineNo, keyword)
		}
		if strings.Contains(role, "_") {
			return failf("Invalid role '%s' on line %d: role names may not contain underscores", role, lineNo)
		}
		if seenRoles[role] {
			return failf("Duplicate role '%s' in %s", role, c.ConfigFile)
		}
		if !isMasterOrNone(worktree) && seenWorktrees[worktree] {
			return failf("Duplicate worktree '%s' in %s", worktree, c.ConfigFile)
		}
		if strings.Contains(worktree, "/") || worktree == "." || worktree == ".." {
			return failf("Invalid worktree '%s' for role '%s'", worktree, role)
		}
		if !supportedAgents[agent] {
			return failf("Unsupported agent '%s' for role '%s'", agent, role)
		}
		if !receiveModes[receiveMode] {
			return failf("Invalid receive mode '%s' for role '%s' on line %d: expected task or batch", receiveMode, role, lineNo)
		}
		promptFile := filepath.Join(c.RolesDir, role+".prompt")
		if !exists(promptFile) {
			return failf("Missing role prompt %s", promptFile)
		}

		worktreePath := c.WorkingDir
		if !isMasterOrNone(worktree) {
			worktreePath = filepath.Join(c.WorktreesDir, worktree)
		}
		rows = append(rows, Role{
			Name:         role,
			Agent:        agent,
			Session:      SessionName(role),
			DisplayName:  DisplayName(role),
			WorktreeName: worktree,
			WorktreePath: worktreePath,
			ReceiveMode:  receiveMode,
			ExtraArgs:    extraArgs,
		})
		seenRoles[role] = true
		if !isMasterOrNone(worktree) {
			seenWorktrees[worktree] = true
		}
	}

	if len(rows) == 0 {
		return failf("No windows defined in %s", c.ConfigFile)
	}
	c.Roles = rows
	return nil
}

// PrepareWorkspace creates state directories and writes the tmux-socket,
// sessions.tsv, and roles.tsv files. (Terminal-adapter helper checks from the
// original are omitted for the Linux/tmux-only port.)
func (c *Context) PrepareWorkspace() error {
	for _, dir := range []string{c.StateDir, c.NotifyDir, c.PromptsDir, c.WorktreesDir, c.TmuxSocketDir, c.DaemonDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(c.TmuxSocketFile, []byte(c.TmuxSocket+"\n"), 0o644); err != nil {
		return err
	}
	if err := c.WriteSessionsFile(); err != nil {
		return err
	}
	return c.WriteRolesFile()
}

// WriteSessionsFile writes .swarmforge/sessions.tsv.
func (c *Context) WriteSessionsFile() error {
	var b strings.Builder
	for i, r := range c.Roles {
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%s\n", i+1, r.Name, r.Session, r.DisplayName, r.Agent)
	}
	return os.WriteFile(c.SessionsFile, []byte(b.String()), 0o644)
}

// SessionRow is one parsed line of sessions.tsv.
type SessionRow struct {
	Role    string
	Session string
	Display string
	Agent   string
}

// ReadSessions reads .swarmforge/sessions.tsv for the given root.
func ReadSessions(root string) ([]SessionRow, error) {
	data, err := os.ReadFile(filepath.Join(root, ".swarmforge", "sessions.tsv"))
	if err != nil {
		return nil, err
	}
	var rows []SessionRow
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := strings.Split(line, "\t")
		get := func(i int) string {
			if i < len(f) {
				return f[i]
			}
			return ""
		}
		// columns: index, role, session, display, agent
		rows = append(rows, SessionRow{Role: get(1), Session: get(2), Display: get(3), Agent: get(4)})
	}
	return rows, nil
}

// WriteRolesFile writes .swarmforge/roles.tsv (the file the handoff tooling reads).
func (c *Context) WriteRolesFile() error {
	var b strings.Builder
	for _, r := range c.Roles {
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Name, r.WorktreeName, r.WorktreePath, r.Session, r.DisplayName, r.Agent, r.ReceiveMode)
	}
	return os.WriteFile(c.RolesFile, []byte(b.String()), 0o644)
}

// AgentStartDelayMS returns the configured inter-agent start delay, defaulting
// to 1500ms when SWARMFORGE_AGENT_START_DELAY_MS is unset or non-numeric.
func AgentStartDelayMS() int {
	if v := os.Getenv("SWARMFORGE_AGENT_START_DELAY_MS"); digits.MatchString(v) {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 1500
}

// DisplayName converts a role name into a Title Cased display name.
func DisplayName(role string) string {
	parts := regexp.MustCompile(`[-_\s]+`).Split(role, -1)
	var words []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		words = append(words, strings.ToUpper(p[:1])+strings.ToLower(p[1:]))
	}
	return strings.Join(words, " ")
}

// SessionName returns the tmux session name for a role.
func SessionName(role string) string { return "swarmforge-" + role }

func isMasterOrNone(worktree string) bool {
	return worktree == "master" || worktree == "none"
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func failf(format string, args ...any) error {
	return &handoff.ExitError{Code: 1, Message: "Error: " + fmt.Sprintf(format, args...)}
}
