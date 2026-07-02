# Skills

Reusable skills for this project's agents — procedures, checklists, and playbooks
that describe HOW to carry out recurring work. Per the Skills constitution article,
agents scan this index and read a skill file when its "Use when" applies.

## Available skills

| Skill | Use when |
|-------|----------|
| example-skill.md | Example only — replace or delete. You want a template for writing a new skill. |

## Adding a skill

1. Copy `example-skill.md` to `swarmforge/skills/<name>.md`.
2. Fill in a specific "Use when" (the trigger) and concrete steps.
3. Add a row to the table above.
4. Commit it before `swarmforge up`, so every agent's worktree includes it.

Skills are read as prompt text, so they work with any agent backend. Keep each
skill tight and actionable — a focused playbook, not a manual.
