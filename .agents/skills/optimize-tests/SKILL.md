---
name: optimize-tests
description: Use when investigating or consolidating redundant tests while preserving coverage and caller-visible security, concurrency, expiry, and lifecycle guarantees.
---

# Optimize Tests

Read the root [instructions](../../../AGENTS.md). A request to investigate is
read-only; edit tests only within the authorized scope. Coverage alone does not
prove that a caller-visible guarantee remains protected.

1. Identify tests with `rg --files -g '*_test.go'` in the requested scope. Read
   the repository's validation targets and go-cov configuration. Run the
   existing coverage gate to record the baseline before authorized edits.
2. Compare assertions, fixtures, exercised behavior, and failure modes. A
   duplicate line or shared path is not sufficient evidence of redundancy.
3. Preserve unique ownership, wrong-app authentication, concurrent result
   consumption, exact expiry, state/nonce validation, unknown-field handover,
   capacity, cancellation, and restart assertions relevant to the scope.
4. Consolidate only tests proven to protect the same outcome. Retain meaningful
   integration and race probes even where statement coverage overlaps.
5. Run the relevant tests and the same coverage gate after each logical batch.
   If behavior protection or coverage decreases, revise only this task's edits;
   preserve concurrent agent work. Do not weaken thresholds to pass the gate.
6. Report the removed redundancy, preserved assertions, observed baseline and
   final results, and any verification limits. Commit the coherent delivery
   through [commit-changes](../commit-changes/SKILL.md) under existing rules.

Use existing test helpers and frameworks. Do not introduce a parallel harness
solely to remove a few tests, and do not commit one file at a time when the
logical change spans several files.
