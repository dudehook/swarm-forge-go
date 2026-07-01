# SwarmForge — Go port

A Go reimplementation of SwarmForge, targeting **Linux + zsh + tmux** only. The
goal is a single static binary with no Babashka/JVM dependency.

Scope decisions:
- Linux/zsh only — the macOS/Windows terminal adapters and the GUI window
  watchdog are **dropped**. Agents are watched via tmux directly.
- Teardown becomes an explicit `swarmforge down` command (replacing the
  window-close trigger + `swarm-cleanup.sh`).
- One binary with subcommands; the agent-facing commands keep their original
  names via PATH shims that `up` will install.

A reference checkout of the runnable `two-pack` branch lives at
`../swarm-forge-two-pack` (a git worktree of this repo) for end-to-end testing.

## Layout

```
go.mod                         module github.com/dudehook/swarmforge
cmd/swarmforge/                single-binary entrypoint + subcommand dispatch
  main.go
  main_test.go                 black-box tests ported from test/swarmforge/handoff_test.clj
internal/project/              project-root discovery, roles.tsv parsing  (handoff_lib.bb)
internal/handoff/              message format: headers, body, sequence lock, TASK/BATCH print
internal/inbox/                inbox state machine (ready_for_next*, done_with_current*)
internal/send/                 draft validation + outbox enqueue (swarm_handoff.bb)
internal/config/               swarmforge.conf parser + context/paths/tmux socket (swarmforge.bb)
internal/launch/               agent command construction, sleep inhibitor, base-index (swarmforge.bb)
internal/daemon/               handoff delivery daemon + stop (handoffd.bb, stop_handoff_daemon.bb)
internal/orchestrator/         up/down: git, worktrees, tmux, shims, daemon, attach (run-main!)
internal/terminal/             open a native terminal window per agent (Alacritty by default)
internal/scaffold/             `swarmforge init` — scaffold a project from a template + commit it
templates/                     canonical template sources (installed to the user templates dir)
```

## Build & test

```sh
go build ./...
go test ./...                 # tmux-dependent launch test auto-skips if tmux absent
go build -o swarmforge ./cmd/swarmforge
```

## Try it

```sh
# Set up a project (templates live in ~/.config/swarmforge/templates, nothing embedded):
swarmforge templates                          # list installed templates
swarmforge init -t coding-pair --agent claude # add SwarmForge to the current repo
swarmforge init --new --dir myproj -t coding-pair --yolo   # or a fresh project
#   init writes swarmforge/, ensures .gitignore, and commits the scaffolding

# Validate a project's config without launching anything:
swarmforge up --dry-run        # parses swarmforge/swarmforge.conf, prints the plan

# Launch headless (no terminal attach), then tear down:
swarmforge up --no-attach
swarmforge down

# Normal interactive launch (attaches your terminal to the first session):
swarmforge up

# Open one native terminal window per agent (Alacritty by default):
swarmforge up --windows        # launch + fan out into per-agent windows
swarmforge windows             # or, against an already-running swarm
#   override the emulator with SWARMFORGE_TERMINAL=<cmd> (falls back to `<term> -e ...`)
```

Real end-to-end with the `claude` backend: point a project's `swarmforge.conf`
at `claude` (the `two-pack` reference uses `codex`), e.g.
`window coder claude master`, then run `swarmforge up`. This starts live Claude
Code agents that edit code and use your account — run it interactively, watching.

## Status

Done (ported + tested):
- [x] `project` — root discovery, roles.tsv, role receive-mode
- [x] `handoff` — header parse/set, body, sequence (mkdir lock), print helpers
- [x] `inbox` — ready-for-next / done-with-current (task + batch), dispatch by mode
- [x] `send` — full draft validation, canonical commit resolution, outbox write
- [x] `config` — swarmforge.conf parser, context/paths, portable tmux socket, state files
- [x] `launch` — claude/codex/copilot/grok command construction, grok perms, extra args,
      sleep inhibitor (Linux), tmux base-index detection
- [x] `daemon` — delivery loop, recipient fan-out, tmux wake, sent/failed archiving, stop
- [x] `orchestrator` — `up` (git init, worktrees, tmux sessions, PATH shims, daemon,
      agent launch, attach) and `down` (stop daemon + kill sessions)
- [x] PATH shims so agents call the original command names (written by `up`)
- [x] CLI: `up`, `down`, `handoffd`, `stop-daemon`, `handoff send`,
      `ready-for-next[-task|-batch]`, `done-with-current[-task|-batch]`

- [x] `up` flags: `--no-attach` (headless), `--dry-run`/`-n` (parse + plan), `--windows`/`-w` (per-agent windows)
- [x] `terminal` — `swarmforge windows` opens one native terminal (Alacritty default,
      `SWARMFORGE_TERMINAL` override) per agent attached to its tmux session
- [x] `scaffold` — `swarmforge init` / `swarmforge templates`: scaffold a project from a
      disk template (user dir: `--templates-dir` > `$SWARMFORGE_TEMPLATES_DIR` >
      `~/.config/swarmforge/templates`), substitute `{{AGENT}}`/`{{PROJECT}}`, optional
      `--yolo`/`--new`/`--force`, then commit the scaffolding. Ships the `coding-pair` template.

Tested:
- handoff subsystem: black-box tests ported from handoff_test.clj (all pass)
- config + launch: tests ported from script_test.clj (all pass)
- daemon + down: live integration smoke test against real tmux (delivery, wake,
  header re-ordering, sent archiving, teardown)
- orchestrator: automated `up --no-attach` + `down` test with a fake agent stub
  (sessions, executable shims, state files, running daemon, teardown) — auto-skips
  without tmux
- shim surface verified against the real two-pack prompts: the 7 command names the
  constitution/role prompts tell agents to run all map to written shims
- config parser validated against the real two-pack swarmforge.conf via `up --dry-run`

Remaining:
- [ ] Real end-to-end `up` with live `claude` agents doing a task (interactive; the
      machinery is verified, this is the user-driven acceptance run)
- [ ] Deferred: local LLM backends (LM Studio/ollama) — see memory note; chosen
      approach is Claude Code via an Anthropic-format proxy
- [ ] Optional polish: colored banners, top-level `--help`

## Mapping from the original scripts

The original Babashka/Clojure sources have been removed from this fork (see them
in the upstream repo or this fork's git history). This table records what each Go
package was ported from.


| Original (.bb)                  | Go                                   |
|---------------------------------|--------------------------------------|
| handoff_lib.bb                  | internal/project + internal/handoff  |
| swarm_handoff.bb                | internal/send                        |
| ready_for_next*.bb              | internal/inbox (ReadyForNext*)       |
| done_with_current*.bb           | internal/inbox (DoneWithCurrent*)    |
| swarmforge.bb                   | internal/config + internal/launch + internal/orchestrator |
| handoffd.bb                     | internal/daemon                      |
| stop_handoff_daemon.bb          | internal/daemon (Stop)               |
| swarm-cleanup.sh                | internal/orchestrator (Down)         |
| swarm-window-watchdog.bb        | dropped (Linux/tmux only)            |
| terminal-adapters/*.sh          | dropped (Linux/tmux only)            |
