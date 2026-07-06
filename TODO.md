# SwarmForge — TODO / feature backlog

A running list of features and polish to add. Grouped by area; not in priority
order. Check items off as they land.

## `init` — template variables & Q&A wizard

- [ ] **Wizard-driven `init`.** Turn `init` into a Q&A that fills in the blanks in
      a template.
  - [ ] Templates declare their variables: a list of `{name, prompt, default?,
        required?}` (extend `manifest.json`, or a sibling `variables.json`).
  - [ ] `init` prompts the user for each variable and substitutes the answers into
        the template's files. Generalizes today's hard-coded `{{HARNESS}}` /
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

## Human-in-the-loop mailbox

- [ ] **A mailbox message addressed to the human operator.** Let an agent send a
      handoff whose recipient is the human, which the daemon surfaces to the
      person instead of waking a tmux agent. Uses: questions the swarm needs
      answered, "task complete" notifications, error / blocked-agent alerts.
  - [ ] **Addressing:** a reserved recipient name (e.g. `human` / `operator`).
        Reserve it in config validation so it can't collide with a real role.
  - [ ] **Reuse the pipeline:** ride the existing handoff/inbox flow and
        `swarmforge send`; the daemon special-cases the reserved recipient and
        routes it to a human channel rather than `tmux send-keys`.
  - [ ] **Message kinds / severity:** `question` (needs a reply), `notice` (FYI,
        e.g. completed task), `error`/`alert` (blocked/failed). Maybe a priority.
  - [ ] **Delivery channels (Linux target):** (a) a dedicated
        `.swarmforge/human-inbox/` the TUI surfaces prominently with an unread
        count; (b) OS notification via `notify-send`; (c) tmux `display-message` /
        bell; (d) append to a log. Likely TUI panel + `notify-send`, configurable.
  - [ ] **Reply path (the two-way part):** a human answer becomes a handoff back
        to the asking agent — daemon writes it into that agent's inbox and wakes
        it locally. Could be `swarmforge reply <msg-id> <text>` or the existing
        send tooling with the human as sender. Decide whether/how the asking agent
        blocks or idles while waiting (ties into receive modes).
  - [ ] **TUI:** a Human Inbox panel + unread badge, folded into the deferred
        "Alerts strip" idea, with an action key to answer inline.
  - [ ] **Open questions:** the reserved recipient name; do questions block the
        sender; notification-backend config; how errors from the daemon *itself*
        (not an agent) are represented; dedupe / rate-limit for noisy notices.

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

## Distributed / networked swarms

- [ ] **Agents across multiple machines.** Let one swarm span hosts (e.g. offload
      some roles to a beefier box, or a machine with a local GPU for `opencode`).
  - [ ] Run a `swarmforge` daemon on each machine; give it a networking protocol so
        daemons exchange handoffs across hosts (today delivery is local-filesystem
        inbox/outbox + local `tmux send-keys` wake).
  - [ ] Design questions to settle:
    - [ ] Transport: TCP/gRPC/HTTP service, SSH, or a shared queue? Auth for it.
    - [ ] Config: which roles live on which host (extend `swarmforge.conf` with a
          host/placement field).
    - [ ] Cross-host delivery: remote daemon receives a message, writes it into its
          local agent's inbox, and wakes that agent locally (wake stays local).
    - [ ] Shared state: `roles.tsv` / recipient→host routing table each daemon needs.
    - [ ] Failure handling: host unreachable, retry/queue, partial swarm.

## Polish

- [ ] Colored banners.
- [ ] Top-level `--help`.

## Testing / acceptance (tracking, not features)

- [ ] Live end-to-end `up` with real agents: a `claude` swarm **and** a
      `local`/opencode swarm against LM Studio.
- [ ] Confirm opencode's TUI honors `--prompt` as the seed message (else pass the
      message positionally).
