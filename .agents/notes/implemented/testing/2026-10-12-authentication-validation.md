# Agent Note: Authentication Validation Policy

Status: implemented

## Problem

An authentication gateway can appear correct in a successful login while failing
at identity validation, replay prevention, expiry, credential revocation, or
concurrent result consumption. Frontend rendering checks alone do not prove that
the Console can create and manage usable configuration against the Go service.
Validation must make these caller-visible properties observable and enforce the
same coverage requirements locally and in CI.

## Decision

[.go-cov.toml](../../../../.go-cov.toml) is the authoritative Go coverage policy:
90% total, 85% package, 80% function, uncovered critical blocks rejected, and race
detection enabled. There are no package, file, or function exclusions.
[The coverage script](../../../../scripts/coverage.sh) pins go-cov `v0.1.0`, clears
inherited policy override variables, uses repository-local Go caches, and writes
coverage artifacts under `.tmp/`. `make coverage` runs that gate with a
three-minute test timeout. `make test` includes the gate and Console unit tests.

Go tests exercise HTTP and store behavior for provider validation, complete raw
identity fields, separate state and browser binding, expiry, failed and wrong-App
claims, concurrent consumption, capacity, credentials, sessions, and lifecycle
ordering. Controlled upstream fixtures provide token, signing-key, and profile
responses, including invalid responses and faults. These tests preserve errors
and observe externally visible effects.

Playwright uses one worker and no automatic retries. It starts a fresh local Go
service with isolated SQLite data and the production Console, and a separate
mockup using production components and styles. Scenarios cover administrator
login, App management, one-time Secret display, rotation and revocation, and
responsive mockup interaction. Existing browsers or repository-local temporary
browser installations prevent task tooling from leaving a permanent installation.

The [configuration reference](../../../../docs/reference/configuration.md) owns
validation commands and limits. The
[task receipt](../../../../docs/tasks/2026-10-12-authentication-gateway.md) owns
observed outcomes and distinguishes fixture coverage from live provider checks.

## Alternatives

No alternative validation policy was selected during this implementation.
Coverage thresholds supplement behavioral assertions; they do not replace
provider fault cases, race detection, or local browser verification.

## Consequences

Changes must satisfy the same checked-in gate, including error paths and critical
blocks. Repository-local caches keep tooling and generated artifacts out of
home-directory configuration folders. Bounded test and browser deadlines expose
hanging work, while failures require diagnosis rather than unchanged retries.

Automated provider fixtures cannot prove production registration, consent,
credentials, account eligibility, or upstream service availability. Real
personal Microsoft, Google, and GitHub authentication requires a deployment
with configured provider Apps and fresh interactive verification.
