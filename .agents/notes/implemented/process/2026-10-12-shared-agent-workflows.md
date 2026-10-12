# Agent Note: Shared Agent Workflows and Decision Records

Status: implemented

## Problem

Repository instructions and agent workflows need a shared, discoverable owner.
Architecture decisions also need durable alternatives and consequences without
mixing them with task execution or validation history.

The UniAuth blueprint selects a Go service and React Console. Its
workflow must preserve that ownership and the requirement that every task's
agents share one worktree.

## Decision

The root [instructions](../../../../AGENTS.md) describe authorization, worktree
collaboration, repository ownership, engineering rules, and validation.
`CLAUDE.md` links to them. `.agents/skills/` owns eight workflows: decision
notes, scope decisions, prose, authoring-session cleanup, commits, PR delivery,
authorized merges, and test optimization. `.claude/skills` is a relative
symlink to the canonical directory.

[Agent Notes](../../README.md) follow lifecycle and class directories.
Implemented decisions go directly to `implemented/`; deliberately deferred
work goes to `proposed/`. Active facts track the delivered system while
decision history preserves the circumstances of the original choice.

The [blueprint](../../../../docs/blueprint/README.md) owns requirements and
architecture. `docs/reference/` describes supported interfaces, gitignored
`docs/plans/` holds execution plans, and `docs/tasks/` retains validation
receipts. Notes preserve detailed alternatives and consequences and link to
those owners without creating a separate requirements hierarchy.

The workflow is adapted from the complete Syntrix `.agents/` inventory, which
contains eight skills and decision notes. UniAuth retains the reusable
conventions and has its own process record. Domain history, PostgreSQL and
Kubernetes requirements, package paths, and Bun scripts belong to their source
project. Git workflows honor current task authorization and standing rules;
mainline pushes and merges each require explicit permission.

## Alternatives

**Keep separate skill copies for each tool.** Tool-specific copies allow
independent discovery but require duplicate policy changes. Relative symlinks
keep one shared owner accessible to both tools.

**Copy the source workflow and notes verbatim.** The source has a complete
workflow, but its product history, package layout, deployment dependencies, and
validation commands describe another system. Adaptation preserves reusable
rules and supplies the correct UniAuth document owners and authorization.

**Keep all decision history in the blueprint.** The blueprint already explains
the agreed mechanism. Detailed alternatives and supersession require a separate
lifecycle so current architecture remains readable while rationale stays
available.

## Consequences

The eight skills and root instructions have canonical paths; tool discovery
uses symlinks and remains independent of a developer's absolute checkout path.
Checkouts must preserve symlinks to use those aliases. Canonical paths remain
directly accessible.

Nontrivial changes include maintained decision records and synchronized active
documents. This adds review and maintenance work. Supersession searches and
explicit archival rules preserve useful history without a duplicate index.

Workflow validation checks metadata, relative links, statuses, required note
sections, and discovery targets. Application validation remains the owning
task's responsibility; structural workflow checks establish no runtime claim.
