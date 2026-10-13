# Agent Note: Docker Compose Releases

Status: implemented

## Problem

The authentication gateway needs a repeatable publication path with staging
validation before production changes. Its SQLite configuration must survive
replacement, while transient login state requires exactly one service process
per environment. A successful staging check must identify the actual artifact
subsequently deployed to production.

## Decision

The [release workflow](../../../../.github/workflows/release.yml) validates and
publishes a Linux AMD64/ARM64 image, then deploys its immutable digest to staging
and prod sequentially. GitHub Environments own deployment ports and approval
rules; provider credentials and administrator hashes remain in environment-specific
VM `.env` files. Each GitHub Environment supplies its directory through
`DEPLOY_PATH`. The reusable deployment job checks out the built revision,
so its Compose definition and scripts travel with that release.

Compose projects have separate SQLite volumes, loopback ports, and runtime files.
The image runs as UID/GID `10001:10001`; fresh volumes inherit the image directory's
ownership. Raw env-file parsing preserves Argon2id dollar signs. Readiness checks
SQLite access, and the stop grace period exceeds the service's shutdown deadline.
SSH verifies a configured host key. Normal completion and handled signals remove
temporary registry credentials; forced SIGKILL can require operator cleanup of
restricted release directories. Per-environment locks and workflow concurrency
serialize deployment changes.
Remote supervision waits on the child through an interruptible Bash builtin. On
signal termination, cleanup removes credentials and kills the isolated deployment
process group, including children that ignore TERM and inherit a deployment lock.

The registry namespace follows the organization's existing Harbor `default`
project convention, while SSH settings use explicit OC VM names. The
[deployment reference](../../../../docs/reference/deployment.md) owns prerequisites,
commands, failure behavior, and operating limitations.

## Alternatives

**Rebuilding for production** would produce another artifact after staging had
validated the earlier build. A digest promotion keeps the tested artifact fixed.

**A shared database volume** would mix staging application credentials with
production and allow staging migrations to change the production registry.
Separate Compose projects provide independent durable state.

**Automatic rollback after a failed readiness check** could run older code against
a schema already migrated by the new process. Recovery requires an explicit
schema-compatible digest or a database restore decision.

## Consequences

Releases depend on organization-variable access, registry credentials, trusted
SSH host-key configuration, prepared VM runtime files, and existing TLS routing.
GitHub Environment reviewers must be configured separately when approval is
required. Runtime credentials stay outside source and build contexts.

Service replacement interrupts transient authentication and administrator sessions.
The readiness gate proves local process/database availability; it does not prove
external TLS routing or interactive upstream authentication. Separate fixture
volumes verify container persistence and isolation without production credentials.
Failed replacement may require operator recovery; database backups and schema
compatibility remain operational responsibilities.
