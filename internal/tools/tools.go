// Package tools owns SwarmForge's built-in knowledge of generic agent
// capabilities and which harnesses provide them natively.
//
// Some capabilities (getting the current time, searching the web, ...) are
// provided natively by some agent harnesses and not by others. To give every
// agent a uniform capability floor without ever telling the agent what harness
// it runs on, SwarmForge installs a fallback *script* into an agent's worktree
// only for the capabilities its harness lacks natively. The agent just sees a
// command on its PATH; it never learns that a capability was polyfilled, and no
// prompt ever mentions the harness. All harness-awareness lives here, in the
// matrix consulted at launch time.
package tools

import (
	"sort"
	"strings"
)

// Capability is a generic agent capability SwarmForge can guarantee by
// installing a fallback script when the agent's harness lacks it natively.
type Capability struct {
	// Name is the command installed on the agent's PATH (e.g. "current-time").
	Name string
	// Description is a one-line summary of what the tool does.
	Description string
	// UseWhen lists the natural-language triggers that should map a task to this
	// tool. It is the intent->tool mapping, written for the model to read.
	UseWhen string
	// Usage shows how to invoke the command.
	Usage string
	// Example shows a concrete invocation.
	Example string
	// Script is the zsh script body installed as the fallback (including the
	// shebang). It must fail loudly (non-zero + usage to stderr) on misuse.
	Script string
}

// Catalog is the built-in set of polyfillable capabilities. It starts
// deliberately small; add entries as fallbacks are written and, for those a
// harness provides natively, record that in nativeByHarness below.
var Catalog = []Capability{
	{
		Name:        "current-time",
		Description: "Print the current local date and time in ISO-8601 (with UTC offset).",
		UseWhen:     `you need the current time or date — "what time is it", "today's date", "timestamp this", "how long since ...".`,
		Usage:       "current-time",
		Example:     "current-time   # -> 2026-07-03T14:22:05-0700",
		Script:      "#!/usr/bin/env zsh\nexec date '+%Y-%m-%dT%H:%M:%S%z'\n",
	},
}

// nativeByHarness records, per harness, the capability names that harness
// provides natively — so SwarmForge does NOT install a fallback for them (the
// harness already advertises the tool to the model in its own schema).
//
// Harnesses whose capabilities depend on the model rather than the binary
// (opencode drives arbitrary OpenAI-compatible models) are intentionally left
// with no native entries: SwarmForge stays conservative and always installs the
// fallback, since a redundant script alongside a native tool is harmless while a
// missing capability is not.
var nativeByHarness = map[string]map[string]bool{
	"claude":   {},
	"codex":    {},
	"copilot":  {},
	"grok":     {},
	"opencode": {},
}

// FallbacksFor returns the catalog capabilities whose fallback script should be
// installed for an agent running harness — i.e. those the harness does not
// provide natively. The result preserves Catalog order.
func FallbacksFor(harness string) []Capability {
	native := nativeByHarness[harness]
	var out []Capability
	for _, c := range Catalog {
		if native[c.Name] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// FallbacksForAll returns the union (by capability name, in Catalog order) of
// the fallbacks needed by any of the given harnesses. Used when several roles
// share one worktree (and therefore one PATH): a capability is installed if any
// sharing harness lacks it natively, so no sharing agent is left short.
func FallbacksForAll(harnesses []string) []Capability {
	needed := map[string]bool{}
	for _, h := range harnesses {
		for _, c := range FallbacksFor(h) {
			needed[c.Name] = true
		}
	}
	var out []Capability
	for _, c := range Catalog {
		if needed[c.Name] {
			out = append(out, c)
		}
	}
	return out
}

// Manifest renders the per-agent README the constitution's tools article tells
// the agent to read. It lists only the shell commands actually installed for
// this agent; capabilities served by a native harness tool are absent by design
// (the harness advertises those itself).
func Manifest(caps []Capability) string {
	var b strings.Builder
	b.WriteString("# Tools\n\n")
	b.WriteString("The following commands are available to you on your PATH. When a task\n")
	b.WriteString("matches a tool's \"Use when\", run that command via your shell.\n")
	if len(caps) == 0 {
		b.WriteString("\n(No SwarmForge tools are installed for this agent.)\n")
		return b.String()
	}
	sorted := append([]Capability(nil), caps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, c := range sorted {
		b.WriteString("\n## " + c.Name + "\n")
		b.WriteString(c.Description + "\n")
		b.WriteString("Use when: " + c.UseWhen + "\n")
		b.WriteString("Usage: `" + c.Usage + "`\n")
		if c.Example != "" {
			b.WriteString("Example: `" + c.Example + "`\n")
		}
	}
	return b.String()
}
