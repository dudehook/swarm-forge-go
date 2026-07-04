package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dudehook/swarmforge/internal/config"
)

func TestProvisionToolsWritesScriptAndManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tools")
	if err := provisionTools(dir, []string{"claude"}); err != nil {
		t.Fatalf("provisionTools: %v", err)
	}

	script := filepath.Join(dir, "current-time")
	info, err := os.Stat(script)
	if err != nil {
		t.Fatalf("current-time not installed: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("current-time should be executable, mode=%v", info.Mode())
	}
	body, _ := os.ReadFile(script)
	if !strings.HasPrefix(string(body), "#!/usr/bin/env zsh") {
		t.Errorf("current-time missing shebang: %q", body)
	}

	manifest, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if !strings.Contains(string(manifest), "## current-time") {
		t.Errorf("manifest should document current-time:\n%s", manifest)
	}
}

func TestMasterHarnessesOnlyMasterRoles(t *testing.T) {
	c := &config.Context{WorkingDir: "/proj"}
	c.Roles = []config.Role{
		{Name: "coder", Agent: "claude", WorktreePath: "/proj"},
		{Name: "cleaner", Agent: "codex", WorktreePath: "/proj/.worktrees/clean"},
		{Name: "lead", Agent: "grok", WorktreePath: "/proj"},
	}
	got := masterHarnesses(c)
	want := map[string]bool{"claude": true, "grok": true}
	if len(got) != 2 {
		t.Fatalf("expected 2 master harnesses, got %v", got)
	}
	for _, h := range got {
		if !want[h] {
			t.Errorf("unexpected master harness %q (codex is on a worktree)", h)
		}
	}
}
