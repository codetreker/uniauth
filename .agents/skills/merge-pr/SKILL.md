---
name: merge-pr
description: Use when verifying a pull request's exact revision and completing an explicitly authorized linear-history merge without bypassing checks.
---

# Verify and Merge a PR

Read the root [instructions](../../../AGENTS.md). A merge requires explicit
user authorization for the current PR state. Prepare and verify the concrete
result before requesting missing permission. Successful checks grant none.

1. Record repository identity, PR number, base, head commit, review state, and
   required checks. Use the task worktree; preserve the author worktree and
   other agents' changes. Do not automatically stash or switch their checkout.
2. Confirm all scoped changes and original review threads are handled. Include
   independent review of introduced critical and major behavior where required.
3. Check the actual head revision's CI result. Diagnose every failure and fix
   in-scope causes. Retry unchanged failures only after establishing an
   infrastructure cause. Recheck the head after any new commit.
4. Refresh the PR and required gates immediately before merging. If existing
   authorization does not cover the result, report its concrete readiness and
   request the missing permission with the policy source.
5. With permission and passing gates, use the repository's permitted linear
   merge method. A squash message retains the PR reference. Never use admin
   bypasses or alter branch protection.
6. Verify the resulting commit and remote mainline state. Remove only a clean
   task-created worktree after its changes are integrated and no agent needs it.
   Do not rewrite a PR squash commit or push additional mainline work.

Use bounded, streaming check monitoring with progress updates. Report the merge
commit, validation, and any unfinished work accurately.
