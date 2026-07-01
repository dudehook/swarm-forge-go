package project

import (
	"os"
	"path/filepath"
	"testing"
)

// Covers roles.tsv parsing that the original handoff_lib.bb exposed via the
// role-known / role-receive-mode / role-worktree-name CLI commands.

func TestRolesTsvLookup(t *testing.T) {
	root := t.TempDir()
	rolesTSV := "coder\tmaster\t" + root + "\tswarmforge-coder\tCoder\tcodex\ttask\n" +
		"cleaner\tcleaner\t" + root + "/.worktrees/cleaner\tswarmforge-cleaner\tCleaner\tcodex\tbatch\n"
	if err := os.MkdirAll(filepath.Join(root, ".swarmforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RolesFile(root), []byte(rolesTSV), 0o644); err != nil {
		t.Fatal(err)
	}

	// Root resolves via the cwd-first check (no git needed).
	if got, err := Root(root); err != nil || got != root {
		t.Fatalf("Root = %q, %v; want %q", got, err, root)
	}

	if known, _ := Known(root, "cleaner"); !known {
		t.Error("cleaner should be known")
	}
	if known, _ := Known(root, "ghost"); known {
		t.Error("ghost should be unknown")
	}
	if mode, _ := ReceiveMode(root, "cleaner"); mode != "batch" {
		t.Errorf("cleaner mode = %q, want batch", mode)
	}
	if mode, _ := ReceiveMode(root, "coder"); mode != "task" {
		t.Errorf("coder mode = %q, want task", mode)
	}
	row, ok, _ := FindRow(root, "cleaner")
	if !ok || row.WorktreeName() != "cleaner" {
		t.Errorf("cleaner worktree = %q (ok=%v)", row.WorktreeName(), ok)
	}
	if _, err := ReceiveMode(root, "ghost"); err == nil {
		t.Error("ReceiveMode for unknown role should error")
	}
}

func TestReceiveModeDefaultsToTask(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".swarmforge"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Row with a trailing empty mode column defaults to "task".
	if err := os.WriteFile(RolesFile(root), []byte("solo\tmaster\t"+root+"\ts\tSolo\tclaude\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mode, _ := ReceiveMode(root, "solo"); mode != "task" {
		t.Errorf("empty mode = %q, want task", mode)
	}
}
