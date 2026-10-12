---
name: commit-changes
description: Use when preparing and committing an authorized task while preserving unrelated work and the repository's Git conventions.
---

# Commit Changes

Follow the root [instructions](../../../AGENTS.md) and existing session
authorization. Work in the task's shared worktree and assign one agent to
prepare commits. A skill invocation does not authorize additional changes.

1. Inspect `git status --short`, `git diff`, `git diff --staged`, and recent
   commit subjects. Establish which files belong to the completed delivery.
2. Verify the relevant checks and synchronized documents, including the owning
   decision note for nontrivial work. Preserve unfinished or unrelated edits.
3. Stage the intended files explicitly. Inspect the complete staged diff and
   run `git diff --cached --check`.
4. Write an English, imperative subject of at most 72 characters, following
   existing commit conventions. Use a concise body for the problem, decision,
   and material validation when useful.
5. Commit with the configured Git identity. Omit tool attribution and AI
   co-author credits. Report the commit and actual validation.
6. Push work-branch commits promptly when covered by the standing authorization
   and available remote. Mainline pushes require their own explicit permission.

Avoid amend unless intentionally revising an existing commit. Preserve author,
committer, and dates when an authorized rewrite requires it. Do not bundle
unrelated completed tasks merely because they share the worktree.
