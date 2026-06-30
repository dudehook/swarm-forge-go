// Package project resolves the SwarmForge project root and reads role metadata
// from .swarmforge/roles.tsv.
//
// It ports the project-root discovery and roles.tsv parsing that appear in the
// original Babashka scripts (handoff_lib.bb, ready_for_next.bb, swarm_handoff.bb).
package project

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoRoot is returned when no SwarmForge project root can be located.
var ErrNoRoot = errors.New("Cannot find SwarmForge project root")

// rolesRelPath is the marker that identifies a project root.
var rolesRelPath = filepath.Join(".swarmforge", "roles.tsv")

// Role returns the current role from SWARMFORGE_ROLE, or an error if unset.
func Role() (string, error) {
	role := os.Getenv("SWARMFORGE_ROLE")
	if role == "" {
		return "", errors.New("Set SWARMFORGE_ROLE.")
	}
	return role, nil
}

// Root locates the project root starting from startDir. It mirrors the
// resolution order used across the Babashka scripts: the directory itself, then
// the git work-tree root, then the parent of the git common directory.
func Root(startDir string) (string, error) {
	if hasRoles(startDir) {
		return startDir, nil
	}
	if top, ok := gitOutput(startDir, "rev-parse", "--show-toplevel"); ok {
		if hasRoles(top) {
			return top, nil
		}
	}
	if common, ok := gitOutput(startDir, "rev-parse", "--git-common-dir"); ok {
		if !filepath.IsAbs(common) {
			if abs, err := filepath.Abs(filepath.Join(startDir, common)); err == nil {
				common = abs
			}
		}
		parent := filepath.Dir(common)
		if hasRoles(parent) {
			return parent, nil
		}
	}
	return "", ErrNoRoot
}

func hasRoles(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, rolesRelPath))
	return err == nil
}

func gitOutput(dir string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// RolesFile returns the path to roles.tsv for the given root.
func RolesFile(root string) string {
	return filepath.Join(root, rolesRelPath)
}

// Row is a parsed roles.tsv record. Fields keeps the raw tab-separated columns
// so callers can read positions the same way the original scripts do.
type Row struct {
	Fields []string
}

// Name is the role name (column 0).
func (r Row) Name() string { return r.field(0) }

// WorktreeName is the worktree assignment (column 1).
func (r Row) WorktreeName() string { return r.field(1) }

// ReceiveMode is the handoff receive mode (column 6), defaulting to "task".
func (r Row) ReceiveMode() string {
	mode := r.field(6)
	if strings.TrimSpace(mode) == "" {
		return "task"
	}
	return mode
}

func (r Row) field(i int) string {
	if i < len(r.Fields) {
		return r.Fields[i]
	}
	return ""
}

// Rows reads and parses every row of roles.tsv under root.
func Rows(root string) ([]Row, error) {
	data, err := os.ReadFile(RolesFile(root))
	if err != nil {
		return nil, err
	}
	var rows []Row
	for _, line := range splitLines(string(data)) {
		rows = append(rows, Row{Fields: strings.Split(line, "\t")})
	}
	return rows, nil
}

// FindRow returns the row for roleName, or false if it is not present.
func FindRow(root, roleName string) (Row, bool, error) {
	rows, err := Rows(root)
	if err != nil {
		return Row{}, false, err
	}
	for _, row := range rows {
		if row.Name() == roleName {
			return row, true, nil
		}
	}
	return Row{}, false, nil
}

// Known reports whether roleName appears in roles.tsv.
func Known(root, roleName string) (bool, error) {
	_, ok, err := FindRow(root, roleName)
	return ok, err
}

// ReceiveMode returns the receive mode for roleName, defaulting to "task".
func ReceiveMode(root, roleName string) (string, error) {
	row, ok, err := FindRow(root, roleName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", errors.New("Unknown role: " + roleName)
	}
	return row.ReceiveMode(), nil
}

// splitLines splits on \n or \r\n and drops trailing empty lines, matching
// clojure.string/split-lines.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	for len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
