# SwarmForge — TODO / feature backlog

A running list of features and polish to add. Grouped by area; not in priority
order. Check items off as they land.

## `init` — template variables & Q&A wizard

- [ ] **Wizard-driven `init`.** Turn `init` into a Q&A that fills in the blanks in
      a template.
  - [ ] Templates declare their variables: a list of `{name, prompt, default?,
        required?}` (extend `manifest.json`, or a sibling `variables.json`).
  - [ ] `init` prompts the user for each variable and substitutes the answers into
        the template's files. Generalizes today's hard-coded `{{AGENT}}` /
        `{{PROJECT}}` substitution into arbitrary `{{VAR}}` tokens.
  - [ ] Non-interactive escape hatch: flags / `--yolo` to take defaults and skip
        the wizard (keep `init` scriptable).
- [ ] **Common built-in questions** the wizard asks regardless of template:
  - [ ] "Enable yolo for all agents?" → append `--yolo` to every `window` line
        (the mechanism already exists in `scaffold.substitute`).
  - [ ] Candidates to consider: default agent/provider, project name & description,
        which worktrees, per-role receive mode.

## TUI

- [ ] **Blue background** for the dashboard, to visually differentiate the
      SwarmForge TUI window from other terminal windows.
- [ ] Deferred TUI ideas (from earlier):
  - [ ] Message drill-down (inspect a single handoff).
  - [ ] Alerts strip (dead agents, delivery failures).
  - [ ] Pipeline view (roles as a flow).
  - [ ] Action keys (attach / down / send from the TUI).
  - [ ] Live tool metrics (needs agents to write results into `.swarmforge`).

## Templates

- [ ] **Rethink template distribution.** Embedding feels wrong for non-static
      templates. Options: pull from the repo / a release asset / a URL on
      `templates install`. Currently only `coding-pair` is embedded as the stable
      starter.
- [ ] **Naming consistency:** rename `coding-pair` → `two-pack` (to match
      `four-pack` / `six-pack`).
- [ ] **Non-coding domain templates** (research, writing, …) — deferred by choice.

## Providers / backends

- [ ] **`copilot --model`** injection — left out because copilot's model flag was
      unconfirmed; verify and wire it like claude/codex/grok.

## Skills

- [ ] **Backend-native Claude Code skills / MCP layer** — verify `~/.claude` vs
      project `.claude` discovery under a worktree cwd, then support it natively.
- [ ] Possibly have `up` copy a skills set into each worktree (like the PATH
      shims) so the Claude-native layer is present per-agent.

## Polish

- [ ] Colored banners.
- [ ] Top-level `--help`.

## Testing / acceptance (tracking, not features)

- [ ] Live end-to-end `up` with real agents: a `claude` swarm **and** a
      `local`/opencode swarm against LM Studio.
- [ ] Confirm opencode's TUI honors `--prompt` as the seed message (else pass the
      message positionally).
