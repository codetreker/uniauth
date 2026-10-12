---
name: pr-push
description: Use when pushing an authorized work branch and preparing or updating its pull request with concrete behavior and observed validation.
---

# Push a Work Branch and Prepare a PR

Follow the root [instructions](../../../AGENTS.md). Skills grant no publishing
permission; honor the user's standing work-branch rules and explicit PR scope.
Never publish mainline changes through this workflow.

1. Inspect branch, status, remote identity, commits, and base. Read the full
   branch diff and confirm it matches the agreed task. Preserve other agents'
   incomplete work; commit only a complete scoped delivery.
2. Push the work branch with its configured upstream or
   `git push -u origin <branch>`. Diagnose errors before retrying. A necessary
   authorized rewrite uses an explicit known-commit `--force-with-lease`; stop
   on a rejected lease and explain the changed remote state.
3. Refresh any existing PR. Update its title and body to describe the final
   delivered behavior; create a PR only within the authorized publication scope.
4. Lead an English PR description with the concrete problem and outcome.
   Include the material design decision, observed validation, and limitations
   needed by a reviewer. Keep its purpose focused.
5. Pass multiline text through a structured argument or write it in `.tmp/`
   and use `gh pr create --body-file <path>`. Clean up the temporary file.
6. Report the branch commit and PR URL, plus any unresolved check or permission
   issue. Diagnose failed CI instead of repeatedly rerunning it.

Review comments require the applicable authorization. Place actionable findings
inline, state conclusions, and keep investigation details in the user report.
Critical and major in-scope fixes remain in this PR; other substantial findings
receive a follow-up disposition.
