# Agent Note: Docker Compose Releases

Status: implemented

## Problem

The gateway needs deliberate image publication followed by staging and prod
deployment, with persistent application configuration isolated between environments.

## Decision

The [release workflow](../../../../.github/workflows/release.yml) starts only
through manual `workflow_dispatch`. Its build job retains the main-ref guard,
restricting image publication and dependent deployments to `main`. Operators
choose when source changes are ready for publication and deployment; merging a
source change does not itself start a release.

The workflow builds one image with a tag containing `sha-` followed by the
first 16 characters of the source commit hash. The shorter tag identifies the
source revision for operators; staging and prod receive the same image digest.
Deployment copies `deploy/docker-compose.yml` as `docker-compose.yml` to the
environment's `DEPLOY_PATH` and runs `docker compose up -d` there. GitHub owns
image publication and file transfer. The VM owns `.env`, persistent volumes,
and Caddy configuration. The [public GHCR decision](2026-10-13-public-ghcr-images.md)
replaces the original VM registry-login prerequisite and owns image access.
The [deployment reference](../../../../docs/reference/deployment.md) owns the
manual startup command and deployment prerequisites.

## Alternatives

**Publishing after every mainline push** couples each merged change to image
publication and deployment. An explicit operator action controls release timing.

**Rebuilding for prod** creates another artifact after staging used the earlier
build. Digest promotion identifies one artifact for both environments.

**A custom remote deployment wrapper** adds credential transport, locks, process
supervision, and cleanup ownership to a file-transfer operation. VM-managed
credentials and the standard Compose command keep those responsibilities local.

## Consequences

Operators initiate a release on `main` when the source is ready; mainline pushes
run their configured checks without initiating deployment. Deployment requires
prepared VM configuration and anonymous GHCR pull access. Separate Compose
project names isolate SQLite volumes. Prod follows successful staging command
execution; `up -d` does not wait for application readiness. Restart loses
transient authentication state while preserving application configuration.
