package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFallbacksForInstallsNonNative(t *testing.T) {
	// current-time is native to no harness in the matrix, so every harness gets
	// it as a fallback.
	for _, h := range []string{"claude", "codex", "copilot", "grok", "opencode"} {
		caps := FallbacksFor(h)
		if !hasCap(caps, "current-time") {
			t.Errorf("harness %q should get the current-time fallback, got %v", h, names(caps))
		}
	}
}

func TestFallbacksForOmitsNative(t *testing.T) {
	// Simulate a harness that provides current-time natively; it must not get a
	// fallback script for it.
	restore := nativeByHarness["claude"]
	nativeByHarness["claude"] = map[string]bool{"current-time": true}
	defer func() { nativeByHarness["claude"] = restore }()

	if caps := FallbacksFor("claude"); hasCap(caps, "current-time") {
		t.Errorf("native current-time should be omitted, got %v", names(caps))
	}
	// A harness without it still receives the fallback.
	if caps := FallbacksFor("codex"); !hasCap(caps, "current-time") {
		t.Errorf("non-native harness should still get the fallback, got %v", names(caps))
	}
}

func TestFallbacksForAllIsUnion(t *testing.T) {
	// One harness native, one not -> the shared PATH still gets the fallback so
	// the non-native agent is not left short.
	restore := nativeByHarness["claude"]
	nativeByHarness["claude"] = map[string]bool{"current-time": true}
	defer func() { nativeByHarness["claude"] = restore }()

	caps := FallbacksForAll([]string{"claude", "codex"})
	if !hasCap(caps, "current-time") {
		t.Errorf("union should include current-time (codex needs it), got %v", names(caps))
	}
}

func TestFallbacksForUnknownHarness(t *testing.T) {
	// An unknown harness has no native-map entry; treat everything as needed.
	if caps := FallbacksFor("mystery"); !hasCap(caps, "current-time") {
		t.Errorf("unknown harness should get every fallback, got %v", names(caps))
	}
}

func TestManifestListsInstalledTools(t *testing.T) {
	m := Manifest(FallbacksFor("claude"))
	for _, want := range []string{"# Tools", "## current-time", "Use when:", "Usage: `current-time`"} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q:\n%s", want, m)
		}
	}
	// Harness-blind: the manifest must never leak harness names.
	for _, h := range []string{"claude", "codex", "opencode", "harness"} {
		if strings.Contains(strings.ToLower(m), h) {
			t.Errorf("manifest leaked harness reference %q:\n%s", h, m)
		}
	}
}

func TestManifestEmpty(t *testing.T) {
	m := Manifest(nil)
	if !strings.Contains(m, "No SwarmForge tools") {
		t.Errorf("empty manifest should say so:\n%s", m)
	}
}

func TestCurrentTimeScriptShape(t *testing.T) {
	var ct *Capability
	for i := range Catalog {
		if Catalog[i].Name == "current-time" {
			ct = &Catalog[i]
		}
	}
	if ct == nil {
		t.Fatal("current-time not in catalog")
	}
	if !strings.HasPrefix(ct.Script, "#!/usr/bin/env zsh\n") {
		t.Errorf("script should start with a zsh shebang: %q", ct.Script)
	}
	if !strings.Contains(ct.Script, "date ") {
		t.Errorf("current-time fallback should call date: %q", ct.Script)
	}
}

// TestCurrentTimeScriptRuns writes the exact catalog script to disk and runs
// it, proving the fallback actually produces an ISO-8601 timestamp. Skips if the
// zsh interpreter it targets is unavailable.
func TestCurrentTimeScriptRuns(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh not available")
	}
	var script string
	for _, c := range Catalog {
		if c.Name == "current-time" {
			script = c.Script
		}
	}
	p := filepath.Join(t.TempDir(), "current-time")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(p).CombinedOutput()
	if err != nil {
		t.Fatalf("running current-time: %v (%s)", err, out)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{4}`).Match(out) {
		t.Errorf("output is not ISO-8601: %q", out)
	}
}

func hasCap(caps []Capability, name string) bool {
	for _, c := range caps {
		if c.Name == name {
			return true
		}
	}
	return false
}

func names(caps []Capability) []string {
	var out []string
	for _, c := range caps {
		out = append(out, c.Name)
	}
	return out
}
