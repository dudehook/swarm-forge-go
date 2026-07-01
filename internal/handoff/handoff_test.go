package handoff

import (
	"os"
	"path/filepath"
	"testing"
)

// Direct coverage for helpers that the original test/swarmforge/*.clj exercised
// via the handoff_lib.bb CLI (next-sequence, set-header, header-field, body).

func TestNextSequenceIncrements(t *testing.T) {
	dir := t.TempDir()
	first, err := NextSequence(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NextSequence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != "000001" || second != "000002" {
		t.Errorf("got %q, %q; want 000001, 000002", first, second)
	}
	// The lock directory must not be left behind.
	if _, err := os.Stat(filepath.Join(StateDir(dir), "sequence.lock")); !os.IsNotExist(err) {
		t.Errorf("sequence.lock should be released")
	}
}

func writeTmp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.handoff")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetHeaderInsertsBeforeBlank(t *testing.T) {
	path := writeTmp(t, "id: 1\nfrom: a\n\nbody\n")
	if err := SetHeader(path, "dequeued_at", "2026-06-16T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := FileHeader(path, "dequeued_at"); !ok || v != "2026-06-16T00:00:00Z" {
		t.Errorf("dequeued_at = %q (ok=%v)", v, ok)
	}
	// Body and existing headers preserved.
	if body, _ := Body(path); body != "body\n" {
		t.Errorf("body = %q", body)
	}
	if v, _, _ := FileHeader(path, "id"); v != "1" {
		t.Errorf("id header lost: %q", v)
	}
}

func TestSetHeaderReplacesExisting(t *testing.T) {
	path := writeTmp(t, "id: 1\nfrom: a\n\nbody\n")
	if err := SetHeader(path, "from", "b"); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := FileHeader(path, "from"); v != "b" {
		t.Errorf("from = %q, want b", v)
	}
}

func TestSetHeaderAppendsWhenNoBlank(t *testing.T) {
	path := writeTmp(t, "id: 1\n")
	if err := SetHeader(path, "x", "y"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := FileHeader(path, "x"); !ok || v != "y" {
		t.Errorf("x = %q (ok=%v)", v, ok)
	}
}

func TestHeaderFieldStopsAtBlank(t *testing.T) {
	// A "task:" line in the body must not be read as a header.
	content := "id: 1\ntype: git_handoff\n\ntask: not-a-header\n"
	if v, ok := HeaderField(content, "type"); !ok || v != "git_handoff" {
		t.Errorf("type = %q (ok=%v)", v, ok)
	}
	if _, ok := HeaderField(content, "task"); ok {
		t.Error("task should not be found in the body")
	}
}
