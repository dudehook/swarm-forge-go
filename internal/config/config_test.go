package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Ported from the launcher-config portions of test/swarmforge/script_test.clj.

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeMinimalProject(t *testing.T, root, conf string, roleFiles ...string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "swarmforge/constitution.prompt"), "Read articles.\n")
	writeFile(t, filepath.Join(root, "swarmforge/swarmforge.conf"), conf)
	for _, r := range roleFiles {
		writeFile(t, filepath.Join(root, "swarmforge/roles", r+".prompt"), r+"\n")
	}
}

func mustParse(t *testing.T, root string) *Context {
	t.Helper()
	c, err := NewContext(root)
	if err != nil {
		t.Fatalf("NewContext: %v", err)
	}
	if err := c.ParseConfig(); err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return c
}

func findRole(c *Context, name string) (Role, bool) {
	for _, r := range c.Roles {
		if r.Name == name {
			return r, true
		}
	}
	return Role{}, false
}

func TestParsesConfigAndWritesStateFiles(t *testing.T) {
	root := t.TempDir()
	writeMinimalProject(t, root,
		"# comment\nwindow coder codex master\nwindow cleaner codex cleaner batch\n",
		"coder", "cleaner")
	c := mustParse(t, root)
	if err := c.PrepareWorkspace(); err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}

	coder, ok := findRole(c, "coder")
	if !ok || coder.DisplayName != "Coder" || coder.Session != "swarmforge-coder" {
		t.Errorf("coder role wrong: %+v", coder)
	}
	cleaner, ok := findRole(c, "cleaner")
	if !ok || cleaner.DisplayName != "Cleaner" || cleaner.ReceiveMode != "batch" {
		t.Errorf("cleaner role wrong: %+v", cleaner)
	}
	if cleaner.Session != "swarmforge-cleaner" {
		t.Errorf("cleaner session = %q", cleaner.Session)
	}
	// coder uses master worktree -> working dir; cleaner uses its own worktree.
	if coder.WorktreePath != c.WorkingDir {
		t.Errorf("coder worktree = %q, want working dir", coder.WorktreePath)
	}
	if cleaner.WorktreePath != filepath.Join(c.WorktreesDir, "cleaner") {
		t.Errorf("cleaner worktree = %q", cleaner.WorktreePath)
	}
	if _, err := os.Stat(c.TmuxSocketFile); err != nil {
		t.Errorf("tmux-socket file not written: %v", err)
	}

	roles, err := os.ReadFile(c.RolesFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(roles), "cleaner\tcleaner\t") {
		t.Errorf("roles.tsv missing cleaner row:\n%s", roles)
	}
}

func TestPortableTmuxSocketDir(t *testing.T) {
	root := t.TempDir()
	writeMinimalProject(t, root, "window coder codex master\n", "coder")
	c := mustParse(t, root)
	if !strings.HasPrefix(c.TmuxSocket, "/tmp/swarmforge-") {
		t.Errorf("socket = %q, want /tmp/swarmforge- prefix", c.TmuxSocket)
	}
	if strings.HasPrefix(c.TmuxSocket, "/private/tmp/") {
		t.Errorf("socket should not be under /private/tmp: %q", c.TmuxSocket)
	}
}

func TestRejectsDuplicateRole(t *testing.T) {
	root := t.TempDir()
	writeMinimalProject(t, root,
		"window coder codex master\nwindow coder codex other\n", "coder")
	c, err := NewContext(root)
	if err != nil {
		t.Fatal(err)
	}
	err = c.ParseConfig()
	if err == nil {
		t.Fatal("expected error for duplicate role")
	}
	if !strings.Contains(err.Error(), "Duplicate role 'coder'") {
		t.Errorf("error = %q, want Duplicate role 'coder'", err.Error())
	}
}

func TestParsesExtraCliArgs(t *testing.T) {
	root := t.TempDir()
	writeMinimalProject(t, root,
		"window coder copilot master --yolo\nwindow cleaner copilot cleaner batch --allow-all-tools\n",
		"coder", "cleaner")
	c := mustParse(t, root)
	coder, _ := findRole(c, "coder")
	if coder.ReceiveMode != "task" || coder.ExtraArgs != "--yolo" {
		t.Errorf("coder = mode %q extra %q, want task --yolo", coder.ReceiveMode, coder.ExtraArgs)
	}
	cleaner, _ := findRole(c, "cleaner")
	if cleaner.ReceiveMode != "batch" || cleaner.ExtraArgs != "--allow-all-tools" {
		t.Errorf("cleaner = mode %q extra %q, want batch --allow-all-tools", cleaner.ReceiveMode, cleaner.ExtraArgs)
	}
}

func TestAgentStartDelayIsConfigurable(t *testing.T) {
	t.Setenv("SWARMFORGE_AGENT_START_DELAY_MS", "")
	if got := AgentStartDelayMS(); got != 1500 {
		t.Errorf("default delay = %d, want 1500", got)
	}
	t.Setenv("SWARMFORGE_AGENT_START_DELAY_MS", "2750")
	if got := AgentStartDelayMS(); got != 2750 {
		t.Errorf("configured delay = %d, want 2750", got)
	}
	t.Setenv("SWARMFORGE_AGENT_START_DELAY_MS", "fast")
	if got := AgentStartDelayMS(); got != 1500 {
		t.Errorf("invalid delay = %d, want 1500", got)
	}
}
