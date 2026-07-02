# How SwarmForge Works

A plain-language tour of the moving parts — agents, prompts, git worktrees, tmux,
and the handoff protocol — and how they combine into a self-driving team of AI
workers. A two-role coding swarm (a `coder` and a `cleaner`) is used throughout as
a running example, but the same machinery applies to any kind of work.

SwarmForge is a tool for running several AI agents as a coordinated team on one
project. Each agent is an ordinary AI command-line assistant (such as Claude Code,
Codex, Copilot, or Grok); SwarmForge assigns each one a defined role, gives it an
isolated copy of the project to work in, and provides a reliable way for the agents
to pass work to one another. It is a single self-contained program — no server and
no database — that coordinates everything through the local filesystem, git, and
tmux.

---

## 1. The one-sentence mental model

> SwarmForge runs several AI agents at once, each in its own isolated copy of a
> shared project, and lets them pass work to each other by dropping messages in
> mailboxes — like a small office where every desk has an inbox and an outbox.

Everything else is detail in service of that idea: keep the agents from stepping on
each other, and give them a dependable way to hand work back and forth.

---

## 2. The cast of concepts

| Concept | What it is | Analogy |
|---|---|---|
| **Role** | A named job (e.g. `coder`, `cleaner`) | A person's job title |
| **Agent** | The AI CLI that performs a role (`claude`, `codex`, …) | The worker filling the role |
| **Constitution + role prompt** | The instructions an agent reads on startup | The employee handbook + job description |
| **Worktree** | An isolated copy of the project files for one role | That worker's private desk/workspace |
| **tmux session** | The terminal each agent runs in | The worker's screen an observer can watch |
| **Handoff** | A message passing work from one role to another | An inter-office memo dropped in a mailbox |
| **Handoff daemon** | A background process that delivers handoffs | The office mail carrier |
| **Inbox / outbox** | Folders of message files per role | Physical in/out trays |

The whole system is just files and processes on one machine. State lives in two
hidden folders inside the project: `.swarmforge/` (swarm state) and `.worktrees/`
(the isolated copies).

---

## 3. Defining a swarm: the config

A swarm is described by a single file, `swarmforge/swarmforge.conf`. A two-role
coding swarm looks like this:

```
# window <role> <agent> <worktree> [task|batch] [extra-cli-args...]
window coder   claude master  --yolo
window cleaner claude cleaner  batch --yolo
```

Each `window` line declares one role:

- **role** — a chosen name, e.g. `coder`, `cleaner`.
- **agent** — which AI CLI runs it (`claude`, `codex`, `copilot`, `grok`).
- **worktree** — which workspace it uses (`master` = the main project directory;
  any other name = its own isolated worktree). Covered in §5.
- **task | batch** — how the role consumes its inbox (default `task`). Covered in
  §6.
- **extra args** — passed through to the agent CLI. `--yolo` marks a role for full
  auto-approval so it can run unattended (SwarmForge translates it to each agent's
  own "don't ask permission" mode).

When the swarm starts, each role becomes a running agent in its own tmux session.

---

## 4. What the agents read

An agent does not inherently know its job. On launch it is handed a small bootstrap
instruction: *"Read `swarmforge/constitution.prompt`, then read everything it
points to; then read `swarmforge/roles/<role>.prompt` and follow it."*

- **`swarmforge/constitution.prompt`** and the files in
  **`swarmforge/constitution/articles/`** are the *shared* rules every agent obeys
  — engineering standards, the workflow, the handoff protocol, and project
  specifics.
- **`swarmforge/roles/<role>.prompt`** is the *per-role* job description — what the
  role owns, what it must not touch, and when and how it hands off.
- **`swarmforge/skills/`** holds reusable *skills* — procedures, checklists, and
  playbooks. A Skills constitution article tells agents to scan
  `swarmforge/skills/README.md` and pull in a skill file when its "Use when"
  matches the task. Skills refine *how* work is done without bloating the base
  prompt, and being plain prompt text they work with any agent backend.

This separation is the source of SwarmForge's flexibility: the **mechanism**
(handoffs, worktrees, tmux) is fixed, while the **behavior** is defined entirely by
editable prompt text. Changing the prompts changes what the swarm does, without
touching any code.

---

## 5. Worktrees

Normally a git repository has **one** working directory: the folder where files are
edited, checked out to one branch at a time. A **git worktree** lets a single
repository have **several** working directories at once, each checked out to its
**own** branch, all sharing the same underlying history (`.git`).

SwarmForge uses worktrees to keep agents from colliding. If two agents edited the
*same* folder at the same time, they would overwrite each other's changes. So each
isolated role gets its own worktree — a private copy of the project on its own
branch.

In the two-role coding example:

```
project/                              <- the "master" worktree (branch: master)
│                                        the CODER works here
├── (project files)
├── .git/
├── .swarmforge/                      <- swarm state (mailboxes, daemon, config)
└── .worktrees/
    └── cleaner/                      <- the CLEANER's worktree (branch: swarmforge-cleaner)
        └── (a separate copy of the project files)
```

- The `coder` role is assigned `master`, so it works directly in the main folder on
  the `master` branch.
- The `cleaner` role is assigned the worktree name `cleaner`, so SwarmForge runs
  `git worktree add` to create `.worktrees/cleaner/` on a new branch
  `swarmforge-cleaner`. The cleaner edits *its* copy, never the coder's.

Parallel work converges through git. Each agent **commits** its changes to its own
branch. When it hands off, the message carries the **commit hash**, and the
instruction to the recipient is literally *"merge_and_process \<sender\>
\<commit\>"* — pull the sender's committed work into the recipient's branch and
continue. Git's merge machinery is what safely recombines the two isolated copies.
(Roles assigned `master` or `none` skip the isolation and share the main directory,
which suits a lead role or a solo agent.)

---

## 6. Handoffs: the heart of it

Handoffs are how work flows between roles. A handoff is a small text file with a
header block. There are two types:

- **`git_handoff`** — "work is committed; merge it and take the next step." It
  carries a `task` name and a `commit` hash.
- **`note`** — a plain message for coordination, not tied to a commit.

### The lifecycle of one handoff

```
  SENDER (e.g. coder)                  DAEMON                 RECIPIENT (e.g. cleaner)
  ───────────────────                  ──────                 ────────────────────────
  1. write a draft file
     (headers only)
  2. run swarm_handoff.sh draft  ──►  validates, assigns
                                      a sequence number,
                                      writes to the sender's
                                      OUTBOX
                                          │
  3.                                      ├─ every ~1s, scans
                                          │  each role's outbox
                                          ▼
                                      delivers a copy into the
                                      recipient's INBOX/new ──►  4. file appears in
                                          │                         inbox/new
                                          ├─ "wakes" the recipient
                                          │   by typing into its
                                          │   tmux session:
                                          │   "You have new handoff
                                          │    mail. run ready_for_next.sh"
                                          │
                                          └─ moves the sender's
                                             copy to SENT
                                                                  5. run ready_for_next.sh
                                                                     → moves the highest-
                                                                       priority item from
                                                                       inbox/new → in_process,
                                                                       prints the task
                                                                  6. do the work, commit
                                                                  7. run done_with_current.sh
                                                                     → in_process → completed,
                                                                       then auto-advances to
                                                                       the next item
                                                                  8. (often) send a handoff back
```

Agents never call each other directly. Each one only ever **writes a draft and runs
a script**, or **runs `ready_for_next` / `done_with_current`**. The daemon and the
filesystem do the rest. That indirection is what makes the system robust: if a
recipient is busy, the message simply waits in its inbox.

### The agent-facing commands

These are the only verbs an agent needs. SwarmForge is a single binary, and each of
these actions is one of its subcommands. Because role and constitution prompts refer
to the commands by their traditional shell-script names, `swarmforge up` writes a
tiny shim for each name onto every agent's PATH; each shim just runs the matching
`swarmforge` subcommand. So an agent invokes a familiar name from within its
worktree and the single binary does the work.

| Command an agent runs | Runs | Meaning |
|---|---|---|
| `swarm_handoff.sh <draft>` | `swarmforge send` | Validate and send a handoff |
| `ready_for_next.sh` | `swarmforge ready-for-next` | Accept the next piece of work (or resume the current one) |
| `done_with_current.sh` | `swarmforge done-with-current` | Mark the current work done, then pull the next |

These shims are generated at launch, not stored in the repository — the repository
holds only the `swarmforge` program and the prompt files, not any operational
scripts.

### Priorities and the inbox state machine

Every handoff has a two-digit **priority**, and the message filename begins with it.
Items are taken in filename order, so **lower numbers are more urgent** (`00` beats
`50`). In the coding example the coder sends to the cleaner at priority `50`, and
the cleaner sends its results back at `00` — so the coder always picks up the
cleaner's return *before* starting anything new.

Each role's inbox is a small state machine with three folders:

```
inbox/new  ──ready_for_next──►  inbox/in_process  ──done_with_current──►  inbox/completed
```

The **receive mode** (from the config) controls how much is taken at once:

- **`task`** — take exactly **one** handoff at a time.
- **`batch`** — take **all** the queued items at the most-urgent priority as a
  single group and process them in one pass.

So if a `task`-mode coder fires off three handoffs, a `batch`-mode cleaner consumes
all three together instead of one at a time — fewer, larger review passes.

---

## 7. tmux: where the agents run

[tmux](https://github.com/tmux/tmux) is a "terminal multiplexer": it runs many
terminal sessions and allows switching between them. SwarmForge gives each role its
own tmux **session** named `swarmforge-<role>`, all on a private socket for the
project (under `/tmp/swarmforge-<user>/…`, recorded in `.swarmforge/tmux-socket`).

Separate sessions (rather than windows within one session) mean each agent has an
independently viewable, independently sized surface — an observer can watch every
agent at once by attaching a terminal to each session.

Ways to watch the agents:

- **All at once, in one native window per agent** — `swarmforge windows` opens one
  terminal window per session (Alacritty by default; the emulator is configurable).
  `swarmforge up --windows` launches the swarm straight into that layout.
- **From a single terminal** — attaching to one session and switching with the tmux
  prefix (`Ctrl-b`), then `s`, lists the sessions to jump between.
- **Directly** — `tmux -S <socket> attach -t swarmforge-<role>`, where the socket
  path is stored in `.swarmforge/tmux-socket`.

tmux is also how the system *wakes* an idle agent: the handoff daemon "types" a
short nudge into the recipient's session, prompting it to run `ready_for_next.sh`.

---

## 8. The lifecycle: from empty folder to running swarm

SwarmForge exposes a small set of commands that take a project from nothing to a
live swarm and back.

**`swarmforge init`** turns a directory into a SwarmForge project. It copies a
*template* — a ready-made `swarmforge/` tree (config, constitution, role prompts)
for a given kind of swarm — into the project, fills in the chosen agent, ensures
`.gitignore` excludes the swarm's state folders, and commits the scaffolding.
Templates live in a templates directory on disk (by default
`~/.config/swarmforge/templates/`), and `swarmforge templates` lists them. `init`
works both for an existing repository (it adds the `swarmforge/` files without
disturbing existing work) and for a brand-new project (`--new` creates and
initializes the directory).

Committing the scaffolding matters: worktrees are created from the latest commit
(`HEAD`), so the role prompts must be committed before launch, or a non-master
agent would start in a worktree that lacks its own instructions.

**`swarmforge up`** launches the swarm:

1. Confirm `git` and `tmux` are available; initialize a git repository if needed.
2. Parse `swarmforge.conf` and create each non-master role's worktree.
3. Write swarm state under `.swarmforge/` and generate the PATH-shim commands
   (thin wrappers that forward to the `swarmforge` binary's subcommands).
4. Create a tmux session per role and start the **handoff daemon**.
5. Launch each agent in its session (staggered slightly).
6. Attach the terminal to the first role's session (or open per-agent windows with
   `--windows`, or return immediately with `--no-attach`). `--dry-run` validates the
   config and prints the plan without launching anything.

**`swarmforge down`** stops the daemon and kills all the sessions. Exiting the lead
agent's session triggers the same teardown automatically.

---

## 9. Where everything lives

```
project/
├── swarmforge/                       # the swarm DEFINITION (human-edited prompts + config)
│   ├── swarmforge.conf               #   roles, agents, worktrees, modes
│   ├── constitution.prompt           #   shared-rules entrypoint
│   ├── constitution/articles/*.prompt#   shared rules (engineering, workflow, handoffs, project)
│   ├── roles/<role>.prompt           #   per-role job descriptions
│   ├── skills/                       #   reusable skills (playbooks) + README index
│   └── scripts/                      #   PATH shims to the swarmforge binary (auto-generated at launch, not committed)
├── .swarmforge/                      # swarm STATE (auto-managed, git-ignored)
│   ├── roles.tsv, sessions.tsv       #   resolved topology
│   ├── tmux-socket                   #   this swarm's tmux socket path
│   ├── daemon/                       #   handoff daemon pid + log
│   └── handoffs/                     #   per-role outbox / sent / failed / inbox/{new,in_process,completed}
└── .worktrees/<name>/                # isolated per-role working copies (git-ignored)
```

---

## 10. Beyond coding

Nothing in the machinery is coding-specific. The mechanism — isolated workspaces,
priority mailboxes, and a delivery daemon — is a generic **pipeline of cooperating
agents over shared files**. Only the *prompts* make a given swarm about code.

Adapting SwarmForge to another domain means changing three things and leaving the
engine alone:

1. **The roles**, in `swarmforge.conf`. A research pipeline might declare
   `researcher`, `factchecker`, and `editor` instead of `coder` and `cleaner`.
2. **Each role's prompt**, in `swarmforge/roles/*.prompt`: what the role owns, what
   it must not do, and whom it hands off to. A researcher gathers sources and hands
   off; a fact-checker verifies claims and hands back or forward; an editor polishes.
3. **The constitution articles** for the domain. The **handoffs** article is
   universal and stays; the engineering/TDD article is replaced with domain
   standards (citation rules, a style guide, a schema, and so on).

Several properties carry over to any domain:

- **Artifacts are just files under git.** Documents, outlines, datasets, notes, and
  configs get the same isolation and merge-on-handoff behavior as source code. The
  `git_handoff` "commit and merge" flow works for a Markdown report exactly as it
  does for a `.py` file.
- **`note` handoffs** cover coordination that isn't a concrete committed artifact
  ("this needs clarification", "approved, proceed").
- **Worktrees pay off** wherever two roles might touch the same files at once. Roles
  that work on clearly separate files can be assigned `master`/`none` to skip the
  isolation.
- **Priorities and batch mode** map naturally onto workflows: a reviewer in `batch`
  mode consolidates several drafts into one review; a low priority number on a
  "revise this" handoff jumps ahead of new drafts.
- **Ownership and gates** generalize. The coder→cleaner loop is a generic
  "producer → quality-gate → producer" pattern that also fits specification,
  research, editing, data-cleaning, and review pipelines.

New kinds of swarm are distributed as **templates**: a template is simply a
`swarmforge/` tree plus a small manifest, dropped in the templates directory, from
which `swarmforge init` can scaffold new projects.

---

## 11. Glossary quick-reference

- **Role** — a named job slot in the swarm.
- **Agent** — the AI CLI (claude/codex/…) running a role.
- **Worktree** — an isolated git working copy on its own branch for one role.
- **Constitution** — shared rules every agent reads on startup.
- **Handoff** — a message file that passes work between roles (`git_handoff` or `note`).
- **Outbox / inbox** — per-role folders the daemon moves messages between.
- **Handoff daemon (`handoffd`)** — background process delivering handoffs and waking recipients.
- **Receive mode** — `task` (one at a time) or `batch` (all top-priority items at once).
- **Priority** — two digits; lower is more urgent.
- **Template** — a reusable `swarmforge/` tree that `swarmforge init` scaffolds into a project.
