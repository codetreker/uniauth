# Agent Note: Docker Compose Releases

Status: implemented

## Problem

The gateway needs image publication followed by staging and prod deployment,
with persistent application configuration isolated between environments.

## Decision

The [release workflow](../../../../.github/workflows/release.yml) builds one image
and deploys its digest to staging, then prod. Deployment copies `docker-compose.yml`
to the environment's `DEPLOY_PATH` and runs `docker compose up -d` there.
GitHub owns image publication and file transfer. The VM owns registry login,
`.env`, persistent volumes, and Caddy configuration.

## Alternatives

**Rebuilding for prod** creates another artifact after staging used the earlier
build. Digest promotion identifies one artifact for both environments.

**A custom remote deployment wrapper** adds credential transport, locks, process
supervision, and cleanup ownership to a file-transfer operation. VM-managed
credentials and the standard Compose command keep those responsibilities local.

## Consequences

Deployment requires prepared VM configuration and registry pull access. Separate
Compose project names isolate SQLite volumes. Prod follows successful staging
command execution; `up -d` does not wait for application readiness. Restart loses
transient authentication state while preserving application configuration.
