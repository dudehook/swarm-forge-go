# SwarmForge Templates

A **template** is a reusable starting point for a swarm. `swarmforge init` copies
a template's `swarmforge/` tree into a project, substitutes a couple of tokens,
and commits the result so every worktree can read the role prompts.

This doc explains the template layout, the substitution parameters, and how to
turn an existing (hand-written, non-templated) `swarmforge/` set into a template.

---

## 1. Anatomy of a template

A template is a directory containing a `manifest.json` and a `swarmforge/`
payload:

```
coding-pair/
├── manifest.json
└── swarmforge/
    ├── swarmforge.conf
    ├── constitution.prompt
    ├── constitution/articles/*.prompt
    ├── roles/<role>.prompt
    ├── skills/README.md
    └── tools/                     # (generated at `up`, not shipped in the template)
```

The `swarmforge/` payload is copied verbatim into `<project>/swarmforge/`, except
for the token substitutions described below. Anything under it is fair game:
prompts, articles, skills, config.

### manifest.json

| Field          | Type       | Purpose                                                                 |
| -------------- | ---------- | ----------------------------------------------------------------------- |
| `name`         | string     | Template id (defaults to the directory name if omitted).                |
| `description`  | string     | One-line summary shown by `swarmforge templates`.                       |
| `defaultAgent` | string     | Agent used for `{{AGENT}}` when `init` isn't given `--agent` (see §2).   |
| `roles`        | string[]   | Role names, shown after scaffolding as a convenience. Informational.    |

Example:

```json
{
  "name": "coding-pair",
  "description": "Two-agent TDD coding loop: coder implements, cleaner refactors.",
  "defaultAgent": "claude",
  "roles": ["coder", "cleaner"]
}
```

---

## 2. Substitution parameters

When `init` copies each file in the `swarmforge/` payload, it runs a **plain
literal string replacement** over the file's contents. There are exactly two
tokens today, plus one special config transform.

### `{{AGENT}}`

Replaced with the agent/harness name for the swarm.

- **Value resolution order:** `--agent <name>` flag → the manifest's
  `defaultAgent` → `"claude"`.
- **Typical use:** the `window` lines in `swarmforge.conf`, so one template can be
  launched on any harness.

```
# swarmforge.conf (template)
window coder   {{AGENT}} master
window cleaner {{AGENT}} cleaner batch
```

```
# after `swarmforge init --template coding-pair --agent codex`
window coder   codex master
window cleaner codex cleaner batch
```

`{{AGENT}}` can hold a bare agent (`claude`, `codex`, `copilot`, `grok`) **or a
declared provider name** — `init` doesn't care; the value is substituted as-is
and validated later by `swarmforge up`. (See how-swarmforge-works.md / the
`provider` directive for provider/model pinning and `opencode`.)

### `{{PROJECT}}`

Replaced with the **base name of the target project directory** (e.g. init'ing
into `/home/me/work/acme` yields `acme`).

- Substituted in every payload file, same as `{{AGENT}}`.
- **Currently unused by the built-in templates** — it's available for you to
  reference in prompts (e.g. in `constitution/articles/project.prompt`) when you
  want the project name to appear in an agent's instructions.

### The `--yolo` config transform (special)

`--yolo` is **not** a token. When `init` is passed `--yolo`, it appends `--yolo`
to every `window ` line in `swarmforge.conf` (only) that doesn't already have it.
This marks each agent for unattended full auto-approval (`up` translates the
marker into the harness's bypass/permission flag). It touches no other file and
no other line.

```
window coder {{AGENT}} master   →   window coder claude master --yolo
```

### What init does *not* substitute

- No other `{{...}}` tokens exist. A `{{FOO}}` you invent is left literal.
- Filenames and directory names are **not** substituted — only file *contents*.
- Substitution is literal `strings.ReplaceAll`, not a template engine: no
  conditionals, defaults, or expressions. (A richer variable/Q&A wizard is a
  backlog item in `TODO.md`.)

---

## 3. What `init` does around substitution

For completeness, a full `swarmforge init` run:

1. Resolves the template from the templates dir (§4) and reads its manifest.
2. Determines `agent` (§2) and `project` (target dir basename).
3. Copies `swarmforge/` into the target, applying `{{AGENT}}` / `{{PROJECT}}`
   substitution and the `--yolo` conf transform.
4. Ensures `.gitignore` contains `.swarmforge/` and `.worktrees/`.
5. `git init` (if needed) and commits **only** `swarmforge/` and `.gitignore`
   (other files are left untouched). Committing matters: non-master worktrees are
   created from `HEAD`, so the role prompts must be in a commit to be visible.

Flags: `--template`/`-t` (name), `--dir` (target project dir, default `.`),
`--agent`, `--yolo`, `--new` (create the target dir), `--force` (overwrite an
existing `swarmforge/`), `--templates-dir`, `--list-templates`.

---

## 4. Where templates live

`init` and `templates` resolve the templates directory in this order:

1. `--templates-dir <path>`
2. `$SWARMFORGE_TEMPLATES_DIR`
3. `~/.config/swarmforge/templates`

The repo's `templates/` holds the canonical source. The binary embeds only
`coding-pair` (see root `templates_embed.go`); `swarmforge templates install`
copies embedded templates into the user dir. The richer `four-pack` / `six-pack`
live in the repo `templates/` for manual copy.

---

## 5. Porting a non-templated swarmforge set into a template

If you have a hand-written `swarmforge/` tree (prompts, articles, conf) and want
it to become a reusable template:

1. **Create the template directory** under your templates dir (or the repo
   `templates/`), and move the existing `swarmforge/` tree inside it:

   ```
   my-template/
   ├── manifest.json
   └── swarmforge/        ← your existing tree
   ```

2. **Add `manifest.json`** with at least `name` and `description`; set
   `defaultAgent` to whatever the set was written for, and list `roles`.

3. **Parameterize the harness.** In `swarmforge.conf`, replace the hard-coded
   agent in each `window` line with `{{AGENT}}`:

   ```
   window coder claude master   →   window coder {{AGENT}} master
   ```

   Leave it hard-coded only if the template is meant to be single-harness.

4. **(Optional) Use `{{PROJECT}}`** anywhere in the prompts where you want the
   project name to appear (it's substituted but referenced by no built-in
   template today).

5. **Check for accidental tokens.** Since substitution is literal, any real
   `{{AGENT}}` / `{{PROJECT}}` text in your prompts will be replaced. That's
   almost always what you want, but be aware.

6. **Don't ship generated dirs.** `swarmforge/scripts/` (PATH shims) and
   `swarmforge/tools/` (capability fallbacks + manifest) are generated by
   `swarmforge up`, not part of a template — leave them out.

7. **Verify:** `swarmforge templates` should list it, and
   `swarmforge init --template my-template --new --templates-dir . --dir <dir>`
   followed by `swarmforge up --dry-run` should validate the config.

That's the whole contract: a directory + `manifest.json` + a `swarmforge/`
payload, with `{{AGENT}}` (and optionally `{{PROJECT}}`) as the only knobs.
