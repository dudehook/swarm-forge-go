// Package scaffold implements `swarmforge init`: it materializes a swarm
// template (a swarmforge/ tree from a templates directory) into a project,
// substitutes a few values, ensures .gitignore, and commits the scaffolding so
// non-master worktrees (created from HEAD) can read the role prompts.
//
// Templates live on disk (nothing is embedded): resolved from --templates-dir,
// then $SWARMFORGE_TEMPLATES_DIR, then ~/.config/swarmforge/templates.
package scaffold

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Manifest is a template's manifest.json.
type Manifest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	DefaultAgent string   `json:"defaultAgent"`
	Roles        []string `json:"roles"`
}

// Template is a resolved on-disk template.
type Template struct {
	Manifest
	Dir string // the template's directory (contains manifest.json and swarmforge/)
}

// TemplatesDir resolves the templates directory: override, else
// $SWARMFORGE_TEMPLATES_DIR, else ~/.config/swarmforge/templates.
func TemplatesDir(override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv("SWARMFORGE_TEMPLATES_DIR"); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "swarmforge", "templates")
	}
	return filepath.Join(home, ".config", "swarmforge", "templates")
}

// List returns the templates found in dir (subdirectories with a manifest.json).
func List(dir string) ([]Template, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t, err := load(filepath.Join(dir, e.Name()))
		if err != nil {
			continue // skip directories that aren't valid templates
		}
		out = append(out, *t)
	}
	return out, nil
}

// Load resolves a template by name from dir.
func Load(dir, name string) (*Template, error) {
	return load(filepath.Join(dir, name))
}

func load(templateDir string) (*Template, error) {
	data, err := os.ReadFile(filepath.Join(templateDir, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("not a template (no manifest.json): %s", templateDir)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("invalid manifest.json in %s: %w", templateDir, err)
	}
	if m.Name == "" {
		m.Name = filepath.Base(templateDir)
	}
	if _, err := os.Stat(filepath.Join(templateDir, "swarmforge")); err != nil {
		return nil, fmt.Errorf("template %s has no swarmforge/ payload", m.Name)
	}
	return &Template{Manifest: m, Dir: templateDir}, nil
}

// InstallTemplates copies every template from src (typically the binary's
// embedded templates tree) into the user templates directory, so `init` and
// `templates` can find them on disk. Existing templates are skipped unless force
// is set (force overwrites file-by-file, leaving any extra local files in place).
// src is rooted so each top-level entry is a template dir (name/manifest.json).
func InstallTemplates(out io.Writer, src fs.FS, override string, force bool) error {
	dest := TemplatesDir(override)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	entries, err := fs.ReadDir(src, ".")
	if err != nil {
		return err
	}
	installed, skipped := 0, 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, err := fs.Stat(src, path.Join(name, "manifest.json")); err != nil {
			continue // not a template
		}
		targetDir := filepath.Join(dest, name)
		if _, err := os.Stat(targetDir); err == nil && !force {
			fmt.Fprintf(out, "  skip  %s (already installed; use --force to overwrite)\n", name)
			skipped++
			continue
		}
		if err := copyEmbeddedDir(src, name, targetDir); err != nil {
			return fmt.Errorf("installing template %q: %w", name, err)
		}
		fmt.Fprintf(out, "  ok    %s\n", name)
		installed++
	}
	fmt.Fprintf(out, "Installed %d template(s) into %s", installed, dest)
	if skipped > 0 {
		fmt.Fprintf(out, " (%d already present)", skipped)
	}
	fmt.Fprintln(out)
	return nil
}

// copyEmbeddedDir writes the srcDir subtree of src onto destDir on disk.
func copyEmbeddedDir(src fs.FS, srcDir, destDir string) error {
	return fs.WalkDir(src, srcDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		dest := filepath.Join(destDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0o644)
	})
}

// Options controls an init run.
type Options struct {
	TargetDir    string
	TemplatesDir string
	TemplateName string
	Agent        string
	Yolo         bool
	New          bool
	Force        bool
}

// Init scaffolds the chosen template into the target project.
func Init(out io.Writer, opts Options) error {
	if opts.TemplateName == "" {
		return fmt.Errorf("no template specified (use --template NAME; see 'swarmforge templates')")
	}
	tmpl, err := Load(TemplatesDir(opts.TemplatesDir), opts.TemplateName)
	if err != nil {
		return err
	}

	agent := firstNonEmpty(opts.Agent, tmpl.DefaultAgent, "claude")
	target, err := filepath.Abs(opts.TargetDir)
	if err != nil {
		return err
	}
	if opts.New {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return err
		}
	} else if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("target directory %s does not exist (use --new to create it)", target)
	}

	swarmDir := filepath.Join(target, "swarmforge")
	if _, err := os.Stat(swarmDir); err == nil && !opts.Force {
		return fmt.Errorf("%s already exists (use --force to overwrite)", swarmDir)
	}

	project := filepath.Base(target)
	if err := copyPayload(tmpl, target, agent, project, opts.Yolo); err != nil {
		return err
	}
	if err := ensureGitignore(target); err != nil {
		return err
	}
	if err := gitScaffold(out, target, tmpl.Name); err != nil {
		return err
	}

	fmt.Fprintf(out, "Scaffolded template %q into %s\n", tmpl.Name, target)
	fmt.Fprintf(out, "  agent: %s", agent)
	if len(tmpl.Roles) > 0 {
		fmt.Fprintf(out, "   roles: %s", strings.Join(tmpl.Roles, ", "))
	}
	if opts.Yolo {
		fmt.Fprint(out, "   (--yolo)")
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next:")
	fmt.Fprintln(out, "  1. Edit swarmforge/constitution/articles/project.prompt with your task.")
	fmt.Fprintln(out, "  2. (optional) Add reusable skills under swarmforge/skills/ (see its README).")
	fmt.Fprintln(out, "  3. Run: swarmforge up")
	return nil
}

// copyPayload walks the template's swarmforge/ tree into target/swarmforge,
// substituting {{AGENT}} and {{PROJECT}} and appending --yolo to conf windows.
func copyPayload(tmpl *Template, target, agent, project string, yolo bool) error {
	srcRoot := filepath.Join(tmpl.Dir, "swarmforge")
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, "swarmforge", rel)
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := substitute(string(data), filepath.Base(path), agent, project, yolo)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.WriteFile(dest, []byte(content), 0o644)
	})
}

func substitute(content, base, agent, project string, yolo bool) string {
	content = strings.ReplaceAll(content, "{{AGENT}}", agent)
	content = strings.ReplaceAll(content, "{{PROJECT}}", project)
	if base == "swarmforge.conf" && yolo {
		var lines []string
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(line, "window ") && !strings.Contains(line, "--yolo") {
				line += " --yolo"
			}
			lines = append(lines, line)
		}
		content = strings.Join(lines, "\n")
	}
	return content
}

func ensureGitignore(target string) error {
	path := filepath.Join(target, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	existing := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		existing[strings.TrimSpace(line)] = true
	}
	var add []string
	for _, want := range []string{".swarmforge/", ".worktrees/"} {
		if !existing[want] {
			add = append(add, want)
		}
	}
	if len(add) == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.Join(add, "\n") + "\n")
	return err
}

// gitScaffold initializes a repo if needed, then commits the swarmforge/ tree
// and .gitignore (only), leaving any other files untouched.
func gitScaffold(out io.Writer, target, templateName string) error {
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		if err := git(target, "init"); err != nil {
			return err
		}
		_ = git(target, "branch", "-M", "master")
	}
	if err := git(target, "add", "swarmforge", ".gitignore"); err != nil {
		return err
	}
	// Nothing staged (e.g. re-running --force with identical content): skip commit.
	if git(target, "diff", "--cached", "--quiet") == nil {
		fmt.Fprintln(out, "No scaffolding changes to commit.")
		return nil
	}
	if err := git(target, "commit", "-m", "Add SwarmForge scaffolding ("+templateName+")"); err != nil {
		return err
	}
	fmt.Fprintln(out, "Committed swarmforge scaffolding.")
	return nil
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
