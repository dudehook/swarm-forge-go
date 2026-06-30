# How SwarmForge Works

A plain-language tour of the moving parts — agents, prompts, git worktrees, tmux,
and the handoff protocol — and how they combine into a self-driving team of AI
workers. The examples use the coding demo, but the last section shows how the same
machinery applies to non-coding work.

---

## 1. The one-sentence mental model

> SwarmForge runs several AI agents at once, each in its own isolated copy of a
> shared project, and lets them pass work to each other by dropping messages in
> mailboxes — like a small office where every desk has an inbox and an outbox.

Everything else is detail in service of that idea: keep agents from stepping on
each other, and give them a reliable way to hand work back and forth.

---

## 2. The cast of concepts

| Concept | What it is | Analogy |
|---|---|---|
| **Role** | A named job (e.g. `coder`, `cleaner`) | A person's job title |
| **Agent** | The AI CLI that performs a role (`claude`, `codex`, …) | The worker filling the role |
| **Constitution + role prompt** | The instructions an agent reads on startup | The employee handbook + job description |
| **Worktree** | An isolated copy of the project files for one role | That worker's private desk/workspace |
| **tmux session** | The terminal "window" each agent runs in | The worker's screen you can look over |
| **Handoff** | A message passing work from one role to another | An inter-office memo dropped in a mailbox |
| **Handoff daemon** | A background process that delivers handoffs | The office mail carrier |
| **Inbox / outbox** | Folders of message files per role | Physical in/out trays |

The whole system is just files and processes on your machine — no server, no
database. State lives in two hidden folders: `.swarmforge/` (swarm state) and
`.worktrees/` (the isolated copies).

---

## 3. Roles and agents: the config

A swarm is defined by one file, `swarmforge/swarmforge.conf`. The demo's is:

```
# window <role> <agent> <worktree> [task|batch] [extra-cli-args...]
window coder   claude master  --yolo
window cleaner claude cleaner  batch --yolo
```

Each `window` line declares one role:

- **role** — `coder`, `cleaner` (names you choose)
- **agent** — which AI CLI runs it (`claude`, `codex`, `copilot`, `grok`)
- **worktree** — which workspace it uses (`master` = the main project dir; any
  other name = its own isolated worktree). More on this next.
- **task | batch** — how it consumes its inbox (default `task`). Explained in §7.
- **extra args** — passed to the agent CLI. `--yolo` here means "auto-approve
  everything" so the agent runs unattended.

When the swarm starts, each role becomes a running agent in its own tmux session.

---

## 4. What the agents actually read

An agent doesn't magically know its job. On launch it's handed a tiny bootstrap
instruction: *"Read `swarmforge/constitution.prompt`, then read everything it
points to; then read `swarmforge/roles/<your-role>.prompt` and follow it."*

- **`swarmforge/constitution.prompt`** + the files in
  **`swarmforge/constitution/articles/`** are the *shared* rules every agent obeys
  — engineering standards, the workflow, the handoff protocol, project specifics.
- **`swarmforge/roles/<role>.prompt`** is the *per-role* job description — what
  this role owns, what it must not touch, and when/how to hand off.

This separation is the key to flexibility: the **mechanism** (handoffs, worktrees,
tmux) is fixed, but the **behavior** is entirely defined by editable prompt text.
Change the prompts and you change what the swarm does — without touching any code.

---

## 5. Worktrees — the part that's new to you

Normally a git repository has **one** working directory: the folder where you edit
files, which is "on" one branch at a time. A **git worktree** lets a single repo
have **several** working directories at once, each checked out to its **own
branch**, all sharing the same underlying history (`.git`).

Why SwarmForge uses them: if two agents edited the *same* folder simultaneously,
they'd clobber each other's files. So each isolated role gets its own worktree —
its own private copy of the project on its own branch.

In the demo:

```
swarmforge-claude-demo/              <- the "master" worktree (branch: master)
│                                       the CODER works here
├── string_calculator.py
├── .git/
├── .swarmforge/                      <- swarm state (mailboxes, daemon, config)
└── .worktrees/
    └── cleaner/                      <- the CLEANER's worktree (branch: swarmforge-cleaner)
        └── string_calculator.py         a separate copy of the same project
```

- The `coder` role is assigned `master`, so it works directly in the main folder
  on the `master` branch.
- The `cleaner` role is assigned the worktree name `cleaner`, so SwarmForge ran
  `git worktree add` to create `.worktrees/cleaner/` on a new branch
  `swarmforge-cleaner`. The cleaner edits *its* copy, never the coder's.

How parallel work converges: each agent **commits** its changes to its own branch.
When it hands off, the message carries the **commit hash**, and the instruction to
the receiver is literally *"merge_and_process \<sender\> \<commit\>"* — i.e. "pull my
committed work into your branch and continue." Git's merge machinery is what lets
the two isolated copies safely recombine. (Roles set to `master` or `none` skip the
isolation and share the main directory — fine for a lead role or a solo agent.)

---

## 6. tmux — watching and switching between agents

[tmux](https://github.com/tmux/tmux) is a "terminal multiplexer": it runs many
terminal sessions under one window and lets you flip between them. SwarmForge gives
each role its own tmux **session** named `swarmforge-<role>`, all on a private
socket for this project (under `/tmp/swarmforge-<you>/…`, recorded in
`.swarmforge/tmux-socket`).

When you run `swarmforge up`, it attaches your terminal to the first role's
session. To look over another agent's shoulder:

- **Switch sessions:** press your tmux prefix (`Ctrl-b` by default), then `s`, and
  pick a session from the list.
- **Or attach one directly:**
  ```sh
  tmux -S "$(cat .swarmforge/tmux-socket)" attach -t swarmforge-cleaner
  ```

tmux is also how the system *wakes* an idle agent: the mail carrier "types" a
nudge into the recipient's session (more below).

---

## 7. Handoffs — the heart of it

This is how work flows between roles. A handoff is just a small text file with a
header block. There are two types:

- **`git_handoff`** — "I committed work; merge it and do your part." Carries a
  `task` name and a `commit` hash.
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
                                                                     → moves newest/highest-
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

The agents never call each other directly. They only ever **write a draft and run
a script**, or **run `ready_for_next` / `done_with_current`**. The daemon and the
filesystem do the rest. That indirection is what makes it robust: if an agent is
busy, the message just waits in its inbox.

### The agent-facing commands

These are the only verbs an agent needs (SwarmForge puts them on each agent's PATH):

| Command | Meaning |
|---|---|
| `swarm_handoff.sh <draft>` | Validate and send a handoff |
| `ready_for_next.sh` | Accept the next piece of work (or resume current) |
| `done_with_current.sh` | Mark current work done, then pull the next |

### Priorities and the inbox state machine

Every handoff has a two-digit **priority**, and the filename starts with it. Items
are taken in filename order, so **lower numbers are more urgent** (`00` beats `50`).
In the demo the coder sends to the cleaner at `50`, and the cleaner sends back at
`00` — so the coder always picks up the cleaner's return *before* anything else.

Each role's inbox is a little state machine with three folders:

```
inbox/new  ──ready_for_next──►  inbox/in_process  ──done_with_current──►  inbox/completed
```

The **receive mode** controls how much it grabs at once:

- **`task`** (the coder): take exactly **one** handoff at a time.
- **`batch`** (the cleaner): take **all** the queued items at the most-urgent
  priority as a single group, and clean them up in one pass.

So if the coder fires off three handoffs, the cleaner consumes all three together
instead of one-by-one — fewer, larger review passes.

---

## 8. Startup and teardown

**`swarmforge up`** runs this sequence:

1. Make sure `git` and `tmux` exist; initialize a git repo here if there isn't one.
2. Parse `swarmforge.conf`; create each non-master role's worktree.
3. Write swarm state under `.swarmforge/` and the PATH-shim commands.
4. Create a tmux session per role; start the **handoff daemon**.
5. Launch each agent in its session (with a small stagger).
6. Attach your terminal to the first role's session.

**`swarmforge down`** stops the daemon and kills all the sessions. Exiting the lead
agent's session triggers the same teardown automatically.

Handy variants: `swarmforge up --dry-run` (validate config, launch nothing) and
`swarmforge up --no-attach` (start it in the background).

---

## 9. Where everything lives

```
your-project/
├── swarmforge/                       # the swarm DEFINITION (you edit this)
│   ├── swarmforge.conf               #   roles, agents, worktrees, modes
│   ├── constitution.prompt           #   shared rules entrypoint
│   ├── constitution/articles/*.prompt#   shared rules (engineering, workflow, handoffs, project)
│   ├── roles/<role>.prompt           #   per-role job descriptions
│   └── scripts/                      #   PATH shims (written at launch)
├── .swarmforge/                      # swarm STATE (auto-managed, git-ignored)
│   ├── roles.tsv, sessions.tsv       #   resolved topology
│   ├── tmux-socket                   #   this swarm's tmux socket path
│   ├── daemon/                       #   handoff daemon pid + log
│   └── handoffs/                     #   per-role outbox / sent / failed / inbox/{new,in_process,completed}
└── .worktrees/<name>/                # isolated per-role working copies (git-ignored)
```

---

## 10. Using SwarmForge for non-coding work

Nothing in the machinery is coding-specific. The mechanism — isolated workspaces +
priority mailboxes + a delivery daemon — is a generic **pipeline of cooperating
agents over shared files**. Only the *prompts* make the demo about code.

To repurpose it, you change three things and leave the engine alone:

1. **Define your roles** in `swarmforge.conf`. A research pipeline might be:
   ```
   window researcher claude master  --yolo
   window factchecker claude review  batch --yolo
   window editor      claude editor  --yolo
   ```
2. **Write each role's prompt** (`swarmforge/roles/*.prompt`): what it owns, what
   it must not do, and when to hand off to whom. E.g. *researcher* gathers sources
   into Markdown and hands off; *factchecker* verifies claims and hands back or
   forward; *editor* polishes.
3. **Rewrite the constitution articles** for your domain. Keep the **handoffs**
   article (the protocol is universal); replace the engineering/TDD article with
   your standards (citation rules, tone guide, schema, whatever).

Some practical notes for general work:

- **Artifacts are just files under git.** Documents, outlines, datasets, notes,
  configs — anything text/file-based gets the same isolation + merge-on-handoff
  benefits as code. The `git_handoff` "commit and merge" flow works for a Markdown
  report exactly as it does for a `.py` file.
- **Use `note` handoffs** for coordination that isn't a concrete committed artifact
  (e.g. "I need X clarified" or "approved, proceed").
- **Worktrees still pay off** whenever two roles might touch the same files at once.
  If your roles work on clearly separate files, you can assign some of them
  `master`/`none` and skip the isolation.
- **Priorities and batch mode** map naturally to workflows: a reviewer in `batch`
  mode consolidates several drafts into one review; a low priority number on a
  "revise this" handoff jumps the queue ahead of new drafts.
- **Think in terms of ownership and gates.** The demo's coder→cleaner loop is a
  generic "producer → quality-gate → back to producer" pattern. Specification,
  research, editing, data cleaning, and review pipelines all fit the same shape.

A good first non-coding experiment: copy the demo, swap the two role prompts and
the project article for a *writer → editor* loop, point both at a `notes/`
directory, and give the writer a topic. You'll see the same handoff cycle move a
document forward instead of code.

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
