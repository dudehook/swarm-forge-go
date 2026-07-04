// Package config parses swarmforge.conf into role definitions and computes the
// per-project context (paths and the tmux socket). It ports the configuration
// and context portions of swarmforge.bb.
//
// macOS/Windows terminal-backend fields and the GUI window-tracking files are
// intentionally omitted: this port targets Linux + zsh + tmux only.
package config

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/dudehook/swarmforge/internal/handoff"
)

var (
	// supportedAgents are the CLI harnesses a `window` line may name directly
	// (without a provider). opencode is intentionally excluded: it needs a URL
	// and model, so it is only reachable via a `provider` line.
	supportedAgents = map[string]bool{"claude": true, "codex": true, "copilot": true, "grok": true}
	// providerBackends are the backends a `provider` line may declare. This is
	// the CLI harnesses plus opencode (the local/HTTP OpenAI-compatible harness).
	providerBackends = map[string]bool{"claude": true, "codex": true, "copilot": true, "grok": true, "opencode": true}
	receiveModes     = map[string]bool{"task": true, "batch": true}
	wordSplit        = regexp.MustCompile(`\s+`)
	digits           = regexp.MustCompile(`^[0-9]+$`)
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
	// Model is the model id to run, when the role resolves through a provider.
	// Empty means "the harness's default model".
	Model string
	// Provider is the name of the provider the role resolved through (empty for a
	// bare-agent window). For opencode it doubles as the opencode provider id.
	Provider string
}

// Provider is a named (backend, url, model) mapping declared by a `provider`
// line. For the CLI harnesses (claude/codex/grok/copilot) URL is unused and the
// mapping just pins a Model; for opencode URL is the OpenAI-compatible endpoint.
type Provider struct {
	Name    string
	Backend string
	URL     string
	Model   string
}

// Context holds resolved paths and state for a project working directory.
type Context struct {
	WorkingDir       string
	ScriptDir        string // directory of shim commands placed on each agent's PATH
	ToolsDir         string // directory of capability tools placed on each agent's PATH
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
	OpenCodeConfig   string // generated opencode.json (OPENCODE_CONFIG for opencode roles)
	SelfExe          string

	WindowBaseIndex int
	PaneBaseIndex   int

	Providers []Provider
	Roles     []Role
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
		ToolsDir:         filepath.Join(swarmDir, "tools"),
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
		OpenCodeConfig:   filepath.Join(stateDir, "opencode.json"),
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
	lines := strings.Split(string(data), "\n")

	providers, err := parseProviders(lines)
	if err != nil {
		return err
	}

	var rows []Role
	seenRoles := map[string]bool{}
	seenWorktrees := map[string]bool{}

	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := wordSplit.Split(line, -1)
		keyword := fields[0]
		if keyword == "provider" {
			continue // collected in parseProviders
		}
		if keyword != "window" {
			return failf("Unknown config directive on line %d: %s", lineNo, keyword)
		}
		if len(fields) < 4 {
			return failf("Invalid config line %d: %s", lineNo, line)
		}
		role := fields[1]
		ref := fields[2]
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

		// Field 3 is either a declared provider name or a bare agent CLI.
		agent := strings.ToLower(ref)
		model := ""
		providerName := ""
		if p, ok := providers[ref]; ok {
			agent = p.Backend
			model = p.Model
			providerName = p.Name
		} else if agent == "opencode" {
			return failf("Role '%s' names opencode directly on line %d: opencode requires a provider (add 'provider <name> opencode <url> <model>' and reference <name>)", role, lineNo)
		} else if !supportedAgents[agent] {
			return failf("Unknown agent or provider '%s' for role '%s' on line %d", ref, role, lineNo)
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
			Model:        model,
			Provider:     providerName,
		})
		seenRoles[role] = true
		if !isMasterOrNone(worktree) {
			seenWorktrees[worktree] = true
		}
	}

	if len(rows) == 0 {
		return failf("No windows defined in %s", c.ConfigFile)
	}
	c.Providers = sortedProviders(providers)
	c.Roles = rows
	return nil
}

// parseProviders collects and validates every `provider` line, keyed by name.
// Grammar: provider <name> <backend> <url> <model>. For non-opencode backends
// <url> is unused (use "-"); for opencode it is the OpenAI-compatible endpoint.
func parseProviders(lines []string) (map[string]Provider, error) {
	providers := map[string]Provider{}
	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := wordSplit.Split(line, -1)
		if fields[0] != "provider" {
			continue
		}
		if len(fields) < 5 {
			return nil, failf("Invalid provider line %d: %s (expected: provider <name> <backend> <url> <model>)", lineNo, line)
		}
		name := fields[1]
		backend := strings.ToLower(fields[2])
		url := fields[3]
		model := fields[4]

		if _, dup := providers[name]; dup {
			return nil, failf("Duplicate provider '%s' on line %d", name, lineNo)
		}
		if supportedAgents[name] || name == "opencode" {
			return nil, failf("Provider name '%s' on line %d conflicts with a built-in agent", name, lineNo)
		}
		if !providerBackends[backend] {
			return nil, failf("Unsupported backend '%s' for provider '%s' on line %d", backend, name, lineNo)
		}
		if model == "" || model == "-" {
			return nil, failf("Provider '%s' on line %d requires a model", name, lineNo)
		}
		if backend == "opencode" && !strings.HasPrefix(url, "http") {
			return nil, failf("Provider '%s' (opencode) on line %d requires an http(s) url, got '%s'", name, lineNo, url)
		}
		providers[name] = Provider{Name: name, Backend: backend, URL: url, Model: model}
	}
	return providers, nil
}

func sortedProviders(providers map[string]Provider) []Provider {
	out := make([]Provider, 0, len(providers))
	for _, p := range providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
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
	if err := c.WriteRolesFile(); err != nil {
		return err
	}
	return c.WriteOpenCodeConfig()
}

// WriteOpenCodeConfig writes .swarmforge/opencode.json registering every
// opencode provider as an OpenAI-compatible endpoint, so opencode roles can be
// launched with `--model <provider>/<model>`. It is a no-op (and removes any
// stale file) when no role uses an opencode provider.
func (c *Context) WriteOpenCodeConfig() error {
	block := map[string]any{}
	for _, p := range c.Providers {
		if p.Backend != "opencode" {
			continue
		}
		block[p.Name] = map[string]any{
			"npm":     "@ai-sdk/openai-compatible",
			"name":    p.Name,
			"options": map[string]any{"baseURL": p.URL},
			"models":  map[string]any{p.Model: map[string]any{"name": p.Model}},
		}
	}
	if len(block) == 0 {
		os.Remove(c.OpenCodeConfig)
		return nil
	}
	doc := map[string]any{
		"$schema":  "https://opencode.ai/config.json",
		"provider": block,
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.OpenCodeConfig, append(data, '\n'), 0o644)
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
