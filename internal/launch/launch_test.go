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
