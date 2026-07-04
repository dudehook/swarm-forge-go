package launch

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dudehook/swarmforge/internal/config"
)

// Ported from the launch-command portions of test/swarmforge/script_test.clj.
// Mirrors test-launch-command!: role "coder", display "Coder", master worktree,
// index 1 (so no teardown trailer).

func launchCommand(t *testing.T, root, agent, extraArgs string) (*config.Context, string) {
	t.Helper()
	c, err := config.NewContext(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.PromptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	row := config.Role{
		Name:         "coder",
		Agent:        agent,
		Session:      "swarmforge-coder",
		DisplayName:  "Coder",
		WorktreeName: "master",
		WorktreePath: c.WorkingDir,
		ReceiveMode:  "task",
		ExtraArgs:    extraArgs,
	}
	cmd, err := Command(c, 1, row)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	return c, cmd
}

func TestLaunchPutsScriptsAndToolsOnPath(t *testing.T) {
	c, cmd := launchCommand(t, t.TempDir(), "claude", "")
	want := "export PATH=" + ShellQuote(c.ScriptDir) + ":" + ShellQuote(c.ToolsDir) + ":$PATH"
	if !strings.Contains(cmd, want) {
		t.Errorf("missing %q in: %s", want, cmd)
	}
}

func TestLaunchWorktreeToolsPath(t *testing.T) {
	c, err := config.NewContext(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.PromptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(c.WorktreesDir, "coderwt")
	cmd, err := Command(c, 1, config.Role{
		Name: "coder", Agent: "claude", DisplayName: "Coder",
		WorktreeName: "coderwt", WorktreePath: wt, ReceiveMode: "task",
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	want := ":" + ShellQuote(filepath.Join(wt, "swarmforge", "tools")) + ":$PATH"
	if !strings.Contains(cmd, want) {
		t.Errorf("missing worktree tools dir on PATH: %s", cmd)
	}
}

func TestCopilotLaunchCommandPassesExtraCliArgs(t *testing.T) {
	_, cmd := launchCommand(t, t.TempDir(), "copilot", "--yolo")
	if !strings.Contains(cmd, "copilot -C ") {
		t.Errorf("missing 'copilot -C ' in: %s", cmd)
	}
	if !regexp.MustCompile(`--name 'SwarmForge Coder' --yolo -i`).MatchString(cmd) {
		t.Errorf("extra args not placed correctly: %s", cmd)
	}
}

func TestGrokLaunchCommandPassesInitialPrompt(t *testing.T) {
	c, cmd := launchCommand(t, t.TempDir(), "grok", "")
	checks := []string{
		"grok --cwd ",
		"--permission-mode acceptEdits",
		`--rules "$(cat `,
		`--verbatim "$(cat `,
		".swarmforge/prompts/coder.md",
	}
	for _, want := range checks {
		if !strings.Contains(cmd, want) {
			t.Errorf("missing %q in: %s", want, cmd)
		}
	}
	if _, err := os.Stat(filepath.Join(c.PromptsDir, "coder.md")); err != nil {
		t.Errorf("instruction file not written: %v", err)
	}
}

func TestGrokUsesBypassPermissionsWithAlwaysApprove(t *testing.T) {
	_, cmd := launchCommand(t, t.TempDir(), "grok", "--always-approve")
	if !strings.Contains(cmd, "--permission-mode bypassPermissions") {
		t.Errorf("expected bypassPermissions in: %s", cmd)
	}
	if !strings.Contains(cmd, "--always-approve") {
		t.Errorf("expected --always-approve in: %s", cmd)
	}
	if strings.Contains(cmd, "--permission-mode acceptEdits") {
		t.Errorf("should not contain acceptEdits: %s", cmd)
	}
}

func TestClaudeDefaultsToAcceptEdits(t *testing.T) {
	_, cmd := launchCommand(t, t.TempDir(), "claude", "")
	if !strings.Contains(cmd, "--permission-mode acceptEdits") {
		t.Errorf("expected acceptEdits by default: %s", cmd)
	}
	if strings.Contains(cmd, "bypassPermissions") {
		t.Errorf("should not bypass without --yolo: %s", cmd)
	}
}

func TestClaudeYoloMapsToBypassAndIsStripped(t *testing.T) {
	_, cmd := launchCommand(t, t.TempDir(), "claude", "--yolo")
	if !strings.Contains(cmd, "--permission-mode bypassPermissions") {
		t.Errorf("expected bypassPermissions with --yolo: %s", cmd)
	}
	if strings.Contains(cmd, "acceptEdits") {
		t.Errorf("should not also pass acceptEdits: %s", cmd)
	}
	// --yolo is a SwarmForge marker; the claude CLI must not receive it.
	if strings.Contains(cmd, "--yolo") {
		t.Errorf("--yolo should be stripped from the claude command: %s", cmd)
	}
}

func TestClaudeKeepsOtherExtraArgs(t *testing.T) {
	_, cmd := launchCommand(t, t.TempDir(), "claude", "--yolo --add-dir /tmp")
	if !strings.Contains(cmd, "--add-dir /tmp") {
		t.Errorf("non-marker extra args should pass through: %s", cmd)
	}
	if strings.Contains(cmd, "--yolo") {
		t.Errorf("--yolo should be stripped: %s", cmd)
	}
}

// launchRole builds a launch command for a fully specified role (allowing
// provider/model fields the simpler launchCommand helper doesn't set).
func launchRole(t *testing.T, row config.Role) (*config.Context, string) {
	t.Helper()
	c, err := config.NewContext(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.PromptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if row.WorktreePath == "" {
		row.WorktreePath = c.WorkingDir
	}
	cmd, err := Command(c, 1, row)
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	return c, cmd
}

func TestOpencodeLaunchCommand(t *testing.T) {
	c, cmd := launchRole(t, config.Role{
		Name: "coder", Agent: "opencode", DisplayName: "Coder",
		WorktreeName: "master", Provider: "local-deepseek", Model: "deepseek-9b",
	})
	checks := []string{
		"export OPENCODE_CONFIG=" + ShellQuote(c.OpenCodeConfig),
		"opencode --dir ",
		"--model 'local-deepseek/deepseek-9b'",
		`--prompt "$(cat `,
	}
	for _, want := range checks {
		if !strings.Contains(cmd, want) {
			t.Errorf("missing %q in: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, "--auto") {
		t.Errorf("should not auto-approve without --yolo: %s", cmd)
	}
}

func TestOpencodeAutoApproveWithYolo(t *testing.T) {
	_, cmd := launchRole(t, config.Role{
		Name: "coder", Agent: "opencode", DisplayName: "Coder",
		Provider: "local", Model: "m", ExtraArgs: "--yolo",
	})
	if !strings.Contains(cmd, "--auto") {
		t.Errorf("expected --auto with --yolo: %s", cmd)
	}
	// --yolo is a SwarmForge marker; opencode must not receive it.
	if strings.Contains(cmd, "--yolo") {
		t.Errorf("--yolo should be stripped: %s", cmd)
	}
}

func TestModelFlagInjectedForCliBackends(t *testing.T) {
	for _, agent := range []string{"claude", "codex", "grok"} {
		_, cmd := launchRole(t, config.Role{
			Name: "coder", Agent: agent, DisplayName: "Coder",
			Provider: "fast", Model: "some-model",
		})
		if !strings.Contains(cmd, "--model 'some-model'") {
			t.Errorf("%s: missing --model flag: %s", agent, cmd)
		}
	}
}

func TestNoModelFlagWithoutProvider(t *testing.T) {
	_, cmd := launchRole(t, config.Role{
		Name: "coder", Agent: "claude", DisplayName: "Coder", WorktreeName: "master",
	})
	if strings.Contains(cmd, "--model") {
		t.Errorf("bare agent should not get --model: %s", cmd)
	}
}

func TestSleepInhibitorCanBeDisabled(t *testing.T) {
	t.Setenv("SWARMFORGE_PREVENT_SLEEP", "0")
	if got := SleepInhibitorPrefix(); got != nil {
		t.Errorf("expected nil prefix when disabled, got %v", got)
	}
}

func TestTeardownTrailerOnLeadAgent(t *testing.T) {
	c, err := config.NewContext(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.PromptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	c.Roles = []config.Role{
		{Name: "coder", Session: "swarmforge-coder"},
		{Name: "cleaner", Session: "swarmforge-cleaner"},
	}
	row := config.Role{Name: "coder", Agent: "codex", DisplayName: "Coder", WorktreePath: c.WorkingDir}
	cmd, err := Command(c, 0, row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "down") || !strings.Contains(cmd, "'swarmforge-coder'") || !strings.Contains(cmd, "'swarmforge-cleaner'") {
		t.Errorf("teardown trailer missing sessions: %s", cmd)
	}
	if !strings.Contains(cmd, "exit $exit_code") {
		t.Errorf("teardown trailer should preserve exit code: %s", cmd)
	}
}
