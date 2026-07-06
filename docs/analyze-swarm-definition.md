# Prompt: audit a SwarmForge swarm definition for viability & template-readiness

Hand the block below to a fresh AI session (Claude Code or similar) to have it
audit an existing `swarmforge/` swarm definition — check whether it will actually
launch and coordinate, and what's needed to turn it into a reusable template.

**How to use:** replace `<DEFINITION_PATH>` with the directory that contains the
`swarmforge/` folder (e.g. `~/work/personal/research-swarm`), then paste the
prompt. The audit is read-only — it reports; it does not change files. If the repo
`docs/how-swarmforge-works.md` and `docs/templates.md` are available, the session
can read them for depth, but the prompt is self-contained without them.

---

BEGIN PROMPT

You are auditing a **SwarmForge swarm definition** at `<DEFINITION_PATH>`. Your job
is to determine (1) whether it can launch at all, (2) whether its agents will
actually coordinate, and (3) what it needs to become a reusable template. **Produce
a written report only — do NOT create, edit, or fix any files in this pass.** After
you report, the user may ask you to fix issues and generate missing files.

Read every file under `<DEFINITION_PATH>/swarmforge/` before judging anything. Do
not assume a file exists because a convention says it should — verify on disk.

## What SwarmForge is

SwarmForge runs several AI agents as a coordinated team on one project. Each agent
is an ordinary agent CLI ("harness" — Claude Code, Codex, Copilot, Grok, or a local
model via opencode) assigned a **role**, given an isolated git worktree, and wired
to pass work to other agents via a filesystem **handoff** mailbox delivered by a
background daemon. A project's swarm lives in a `swarmforge/` directory. Coordination
is through the local filesystem, git worktrees, and tmux — no server, no database.

## Hard requirements to launch (`swarmforge up` parses & validates these)

These are blocking — if any fails, the swarm will not start:

1. **`swarmforge/swarmforge.conf`** exists and defines **at least one** `window`.
   - Grammar (whitespace-separated; `#` comments and blank lines ignored):
     `window <role> <harness|provider> <worktree> [task|batch] [extra-cli-args...]`
     and optionally `provider <name> <backend> <url> <model>`.
   - `<role>`: must **not** contain underscores.
   - `<harness>`: one of `claude`, `codex`, `copilot`, `grok`, **or** a declared
     provider name. `opencode` is only reachable via a `provider` line (it needs a
     url + model).
   - `<worktree>`: `master` or `none` means "the repo root"; any other value is a
     worktree name and must contain no `/` and not be `.`/`..`. Non-`master`/`none`
     worktree names must be **unique** across windows.
   - receive mode defaults to `task`; `batch` means the role consumes all currently
     queued equal-priority handoffs at once.
2. **`swarmforge/constitution.prompt`** exists. (Convention: it says "read and obey
   every file in `swarmforge/constitution/articles/`", so every file in `articles/`
   becomes shared context — any extension is read, not just `.prompt`.)
3. For **every** `window` role, **`swarmforge/roles/<role>.prompt`** exists.

## Coordination model — the non-obvious viability checks

Launching is not the same as working. Check these or the swarm starts but stalls:

4. **One role per worktree (mailbox isolation).** The daemon delivers each
   recipient's handoffs into **that role's worktree**:
   `<worktreePath>/.swarmforge/handoffs/inbox/`. Two roles sharing a worktree share
   one inbox *and* outbox directory, which conflates their handoffs. The safe,
   standard topology is: **exactly one entry role on `master`/`none`** (the repo
   root) and **every other role in its own uniquely-named worktree**. Flag any
   definition that puts two roles on `master`/`none`.
5. **No dangling handoff recipients.** `send` validates every recipient against the
   defined roles; a handoff addressed to a role that is not a `window` **errors at
   runtime**. Scan every role prompt for the names of roles it hands off to (e.g.
   "hand off to X", "git_handoff to X", priority routing). Every such name must be a
   defined `window`. A role prompt that references a role which doesn't exist (e.g. a
   `strategist` that was never defined) is a defect — the pipeline will fail when it
   reaches that handoff.
6. **Handoff protocol is taught.** SwarmForge installs PATH shims into each worktree
   at launch — `swarm_handoff.sh`, `ready_for_next[_task|_batch].sh`,
   `done_with_current[_task|_batch].sh`, `stop_handoff_daemon.sh` — but the agents
   only use them correctly if the definition explains the protocol: how to send a
   handoff, priorities, and that work moves between worktrees **via git commits**
   carried on the handoff (the recipient syncs to the sender's commit). The built-in
   templates include an `articles/handoffs.prompt` article and a
   `swarmforge/handoff-protocol.md`. If the role prompts assume handoffs/priorities/
   `git_handoff` but no such article or protocol doc exists, flag it — coordination
   will be unreliable.

## Context wiring (what the agents actually see)

7. **Reachability.** At startup each agent reads `constitution.prompt` recursively,
   then `roles/<role>.prompt` recursively. Anything an agent must know has to be
   reachable from one of those. Check that: `constitution.prompt` pulls in the
   `articles/`; articles/roles reference files that exist; there are no references to
   missing articles, schemas, or docs.
8. **Skills.** Reusable playbooks live in `swarmforge/skills/` with a `README.md`
   index. Agents use a skill either because an `articles/skills.prompt` tells them to
   scan the index, or because a role prompt names specific skills directly. Verify
   that every skill a role prompt references exists in `skills/`, and that skills are
   discoverable (via the article or direct reference).
9. **Tools & native harness capabilities.** At launch SwarmForge generates
   `swarmforge/tools/` (capability scripts + a manifest) based on a per-harness
   capability matrix, and an `articles/tools.prompt` article tells agents to read
   that manifest. Generic capabilities a harness provides **natively** (e.g. Claude
   Code's `WebSearch`/`WebFetch`) are used directly; capabilities a harness lacks are
   polyfilled with a fallback script. Implication for viability: **if a role prompt
   depends on native tools** (e.g. declares `tools: WebSearch, WebFetch` or says
   "search the web"), the swarm is only fully capable on a harness that actually has
   them. Note which harness the definition assumes, and whether that constrains the
   appropriate default harness.

## Template-readiness (turning the definition into a reusable template)

A SwarmForge **template** is a directory containing a `manifest.json` plus a
`swarmforge/` payload. `swarmforge init` copies the payload into a new project,
does a literal string substitution, and commits it. Check:

10. **`manifest.json`** exists (or note that it must be created) with:
    `name`, `description`, `defaultHarness` (the harness used when `init` isn't given
    `--harness`; must be one that supports what the roles need), and `roles` (the
    role names).
11. **Substitution tokens.** Only two exist, applied as literal `ReplaceAll` to every
    payload file: `{{HARNESS}}` (→ `--harness` value, else `defaultHarness`, else
    `claude`) and `{{PROJECT}}` (→ the target directory's base name). For a template,
    the harness in each `window` line should be `{{HARNESS}}`, **not** a hard-coded
    agent. Flag any hard-coded harness in the conf.
12. **Exclude generated / runtime dirs from the payload.** These are produced by
    `swarmforge up` and must NOT be shipped in a template: `swarmforge/scripts/`
    (PATH shims), `swarmforge/tools/` (generated capability scripts + manifest), and
    the runtime state `.swarmforge/` and `.worktrees/`. If the definition is a *run*
    project, it may also contain project artifacts (built code, data) that don't
    belong in the template.
13. **Genericize the task.** `articles/project.prompt` (or wherever the concrete task
    lives) should describe a *generic* starting point, not one specific run's task,
    if the template is meant to be reused. Note anything project-specific baked in.

## Your report

Produce a written viability report with these sections. Do not change any files.

- **Summary** — one line: launchable now? / launchable after fixes / not viable, and
  template-ready? / needs work.
- **Blocking issues** — each requirement in §1–3 that fails (won't launch). Cite the
  file and what's missing/wrong.
- **Coordination warnings** — §4–6 problems (launches but won't coordinate): shared
  worktrees, dangling handoff recipients, missing handoff protocol. Name the exact
  offending roles/lines.
- **Context/wiring gaps** — §7–9: unreachable or missing articles/skills, undiscovered
  skills, native-tool assumptions and the harness they imply.
- **Template-readiness** — §10–13: missing/incomplete `manifest.json`, hard-coded
  harness to tokenize, generated/runtime dirs to exclude, project-specific content to
  genericize.
- **Punch list** — a concrete, ordered list of files to create or edit and what each
  needs, so the user can decide what to fix next. (List them; do not create them.)
- **Recommended defaults** — a proposed `swarmforge.conf` (with `{{HARNESS}}` and a
  valid one-role-per-worktree topology) and a proposed `manifest.json`, presented as
  suggestions for the user to confirm.

END PROMPT
