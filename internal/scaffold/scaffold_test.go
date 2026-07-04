package scaffold

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallTemplates(t *testing.T) {
	src := t.TempDir()
	makeTemplate(t, src, "coding-pair")
	makeTemplate(t, src, "four-pack")
	// A non-template directory must be ignored (no manifest.json).
	if err := os.MkdirAll(filepath.Join(src, "notatemplate"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	var out bytes.Buffer
	if err := InstallTemplates(&out, os.DirFS(src), dest, false); err != nil {
		t.Fatalf("InstallTemplates: %v", err)
	}
	for _, name := range []string{"coding-pair", "four-pack"} {
		if _, err := os.Stat(filepath.Join(dest, name, "manifest.json")); err != nil {
			t.Errorf("template %s not installed: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dest, name, "swarmforge", "swarmforge.conf")); err != nil {
			t.Errorf("template %s payload not installed: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "notatemplate")); !os.IsNotExist(err) {
		t.Errorf("non-template dir should not be installed")
	}
	// The installed templates are now discoverable by List.
	list, err := List(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Errorf("List after install = %d templates, want 2", len(list))
	}
}

func TestInstallTemplatesSkipsThenForces(t *testing.T) {
	src := t.TempDir()
	makeTemplate(t, src, "coding-pair")
	dest := t.TempDir()

	if err := InstallTemplates(io.Discard, os.DirFS(src), dest, false); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(dest, "coding-pair", "swarmforge", "constitution.prompt")
	writeFile(t, edited, "LOCAL EDIT\n")

	// Without --force: skipped, the local edit is preserved.
	var out bytes.Buffer
	if err := InstallTemplates(&out, os.DirFS(src), dest, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "skip") {
		t.Errorf("expected a skip message, got: %s", out.String())
	}
	if data, _ := os.ReadFile(edited); string(data) != "LOCAL EDIT\n" {
		t.Errorf("edit should be preserved without --force, got %q", data)
	}

	// With --force: the file is overwritten from source.
	if err := InstallTemplates(io.Discard, os.DirFS(src), dest, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(edited); string(data) == "LOCAL EDIT\n" {
		t.Errorf("--force should overwrite the edited file")
	}
}

func gitEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// makeTemplate creates a minimal but valid template under dir/name.
func makeTemplate(t *testing.T, dir, name string) {
	t.Helper()
	root := filepath.Join(dir, name)
	writeFile(t, filepath.Join(root, "manifest.json"),
		`{"name":"`+name+`","description":"test template","defaultHarness":"claude","roles":["coder","cleaner"]}`)
	writeFile(t, filepath.Join(root, "swarmforge", "swarmforge.conf"),
		"window coder {{HARNESS}} master\nwindow cleaner {{HARNESS}} cleaner batch\n")
	writeFile(t, filepath.Join(root, "swarmforge", "constitution.prompt"), "Read articles.\n")
	writeFile(t, filepath.Join(root, "swarmforge", "roles", "coder.prompt"), "You are the coder of {{PROJECT}}.\n")
	writeFile(t, filepath.Join(root, "swarmforge", "constitution", "articles", "project.prompt"), "# Project Rules\n")
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestListAndLoad(t *testing.T) {
	dir := t.TempDir()
	makeTemplate(t, dir, "coding-pair")
	// a non-template subdir should be ignored
	if err := os.MkdirAll(filepath.Join(dir, "notatemplate"), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := List(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "coding-pair" {
		t.Fatalf("List = %+v, want one coding-pair", list)
	}
	if list[0].Description != "test template" {
		t.Errorf("description = %q", list[0].Description)
	}
}

func TestInitScaffoldsSubstitutesAndCommits(t *testing.T) {
	gitEnv(t)
	tmplDir := t.TempDir()
	makeTemplate(t, tmplDir, "coding-pair")
	target := t.TempDir()

	var out bytes.Buffer
	err := Init(&out, Options{
		TargetDir:    target,
		TemplatesDir: tmplDir,
		TemplateName: "coding-pair",
		Harness:      "claude",
		Yolo:         true,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	conf, _ := os.ReadFile(filepath.Join(target, "swarmforge", "swarmforge.conf"))
	if !strings.Contains(string(conf), "window coder claude master --yolo") {
		t.Errorf("conf not substituted/yolo'd:\n%s", conf)
	}
	coder, _ := os.ReadFile(filepath.Join(target, "swarmforge", "roles", "coder.prompt"))
	if !strings.Contains(string(coder), "coder of "+filepath.Base(target)) {
		t.Errorf("PROJECT not substituted: %s", coder)
	}
	gi, _ := os.ReadFile(filepath.Join(target, ".gitignore"))
	if !strings.Contains(string(gi), ".swarmforge/") || !strings.Contains(string(gi), ".worktrees/") {
		t.Errorf(".gitignore missing entries:\n%s", gi)
	}
	// Scaffolding was committed and swarmforge/ is in HEAD.
	files := gitOut(t, target, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(files, "swarmforge/swarmforge.conf") {
		t.Errorf("swarmforge/ not committed:\n%s", files)
	}
}

func TestInitExistingRepoLeavesOtherFilesUncommitted(t *testing.T) {
	gitEnv(t)
	tmplDir := t.TempDir()
	makeTemplate(t, tmplDir, "coding-pair")
	target := t.TempDir()

	// Pre-existing repo with an uncommitted file.
	if err := exec.Command("git", "-C", target, "init").Run(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(target, "existing.txt"), "my work in progress\n")

	var out bytes.Buffer
	if err := Init(&out, Options{TargetDir: target, TemplatesDir: tmplDir, TemplateName: "coding-pair"}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// swarmforge/ committed...
	tracked := gitOut(t, target, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(tracked, "swarmforge/swarmforge.conf") {
		t.Errorf("swarmforge/ should be committed:\n%s", tracked)
	}
	// ...but the user's file left untracked (not swept into the commit).
	if strings.Contains(tracked, "existing.txt") {
		t.Error("existing.txt should NOT have been committed")
	}
	status := gitOut(t, target, "status", "--porcelain")
	if !strings.Contains(status, "existing.txt") {
		t.Errorf("existing.txt should still be untracked, status:\n%s", status)
	}
}
