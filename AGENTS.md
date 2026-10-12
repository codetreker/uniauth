# AI Agent Instructions

Read applicable subdirectory `AGENTS.md` files before working there. Explicit
session instructions take precedence over repository workflows.

## Language and authorization

- Discuss work with the user in Chinese. Write code, documents, comments,
  commit messages, and agent communications in English.
- Discussion and investigation requests authorize read-only work. Implementation
  begins after the user authorizes its scope. Honor authorization already given
  for the agreed implementation and dependencies.
- Skills describe workflows; they do not grant permission to edit, publish,
  merge, or push mainline branches. Resolve routine choices within the approved
  scope and ask only when a material decision lacks authorization.
- Report observed checks and results, distinguishing verified behavior from
  assumptions and remaining work.

## Workspace and collaboration

All changes use a task worktree under `.worktrees/<task>`. One task has one
worktree shared by its agents; assign file ownership and preserve others' edits.
Use repository-local `.tmp/` for temporary files and clean up task processes.
Do not place project files in home-directory configuration folders.

Research uses delegated agents with evidence and resolvable sources. Code
review uses independent reviewers and verifies each engineering rule below.
Review findings must include a deferred disposition as well as in-scope fixes
and unsupported findings. Fix confirmed critical and major issues within the
agreed scope; record other substantive work for follow-up. An existing PR's
scope remains fixed unless delivery would otherwise be incorrect.

## Repository map and document ownership

Source directories below describe the agreed target layout. Workflow setup
establishes the instructions and documents; source delivery belongs to its
authorized implementation task.

| Path | Responsibility |
| --- | --- |
| `cmd/uniauth/` | Configuration composition, startup, and service lifecycle |
| `internal/apps/` | SQLite application registry and credential lifecycle |
| `internal/providers/` | Microsoft, Google, and GitHub authentication adapters |
| `internal/auth/` | Login transactions and one-time identity-result handover |
| `internal/console/` | Administrator authentication and management API |
| `web/console/` | React and TypeScript administrator interface |
| `docs/blueprint/` | Agreed architecture and requirements |
| `docs/reference/` | Implemented APIs, configuration, and observable behavior |
| `docs/plans/` | Gitignored execution plans for authorized tasks |
| `docs/tasks/` | Retained task and validation receipts |
| `.agents/notes/` | Durable decisions, alternatives, and consequences |
| `.agents/skills/` | Canonical repository-local skills |

[UniAuth Blueprint](docs/blueprint/README.md) owns the architecture. The service
has no persistent end-user store or central SSO session. Products own user
records, account linking, authorization, and login sessions. Identity handover
uses an App-bound result kept only in process memory for five minutes, then
atomically consumed once. SQLite stores application configuration; upstream
provider credentials come from deployment configuration. Console administrator
authority is independent of product credentials.

[Agent Notes](.agents/notes/README.md) owns the decision-record lifecycle and
[its local instructions](.agents/notes/AGENTS.md) govern note editing. Plans
describe execution; task reports preserve evidence. Keep blueprint requirements
and implemented reference behavior distinct.

## Engineering and validation

- Search existing capabilities and affected documents before changing code.
  Reuse suitable mechanisms, explain missing contracts, and assess architecture
  across the authorized scope before adding abstractions. Use interfaces when
  multiple actual implementations require them.
- Rewrite affected active documents coherently and synchronize related owners
  in the same change. Explicit historical archives remain frozen. Deliver a
  nontrivial change with an implemented note or update its existing owner.
- Propagate errors with their original cause. Do not fabricate empty provider
  data, silently substitute identity keys, or add unsupported fallback values.
  Preserve unknown JSON fields, nulls, and false values in identity sources.
- Comments explain non-obvious decisions, evidence, ordering, or ownership.
  Use the author's voice, keep research references durable, and avoid narrating
  obvious code or referring to temporary drafts.
- Persistently stored or wire-format compatibility requires an explicit decision;
  migrate old persisted data when needed. Adding foreign keys requires discussion.
- Use Go community conventions and establish an authoritative go-cov policy
  with implementation. Test caller
  behavior, expiry boundaries, concurrent consumption, errors, and lifecycle
  properties rather than mirroring implementation details.
- Frontend development uses pnpm, TypeScript, modern ES, arrow functions, and
  package-relative imports. Mockups use the final components and styles, include
  representative data states, and permit frontend interaction.
- Run appropriate validation after changes and complete the repository gates.
  Give hanging commands and tests bounded deadlines. Diagnose failed checks;
  retry unchanged failures only after proving an infrastructure cause. Keep
  long-running output streaming and clean up background resources.
- For workflow changes, validate skill metadata, relative links, note lifecycle,
  and discovery symlinks. Checks do not establish untested application behavior.

## Git and review delivery

- Use the repository's commit style, English messages, and default Git identity.
  Omit AI co-author credits and machine-specific checkout paths.
- On work branches, commit and push completed changes promptly under the user's
  standing rules. Preserve unrelated or unfinished agent work; stage the owned
  delivery deliberately. Accept mainline updates with merge when required.
- Mainline pushes and PR merges each require explicit user authorization. Keep
  mainline history linear. Never bypass required checks or protection rules.
- Avoid work-branch amend and force push unless intentionally rewriting a
  commit. An authorized force push uses `--force-with-lease` bound to the known
  remote commit; a rejected lease requires a new user decision.
- PR descriptions explain the concrete behavior and observed validation.
  Publish only actionable review conclusions, using inline comments where
  possible. Respond to findings in their original threads and resolve handled
  threads when authorized. Preserve the PR reference in squash commits.

## Shared skills and discovery

`.agents/skills/` is canonical. `.claude/skills` links to it and `CLAUDE.md`
links to this file. Tool aliases introduce no additional policy.

| Skill | Use |
| --- | --- |
| [agent-notes](.agents/skills/agent-notes/SKILL.md) | Write, review, move, or prune decision records |
| [scope-cut](.agents/skills/scope-cut/SKILL.md) | Evaluate actual deferrals and preserve required outcomes |
| [prose-standard](.agents/skills/prose-standard/SKILL.md) | Preserve complete contracts in concise prose |
| [trim-cot-leakage](.agents/skills/trim-cot-leakage/SKILL.md) | Remove transient authoring-session narration |
| [commit-changes](.agents/skills/commit-changes/SKILL.md) | Prepare and create scoped commits |
| [pr-push](.agents/skills/pr-push/SKILL.md) | Push work branches and prepare authorized PRs |
| [merge-pr](.agents/skills/merge-pr/SKILL.md) | Verify checks and perform an authorized merge |
| [optimize-tests](.agents/skills/optimize-tests/SKILL.md) | Consolidate redundant tests without losing guarantees |
