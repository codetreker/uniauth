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

Actions run titles identify the workflow run number, staging-to-prod flow,
branch, and initiating actor, such as
`Release #12 · staging → prod · main · @alice`. The title is defined with
`run-name` from the supported GitHub context; the run number and original actor
remain stable on a rerun. This identifies a pending run before the build job
allocates its image tag.

The workflow builds one image with a tag in `<yymmdd>-<seq>-<hash-8>` format.
The tag combines the allocation step's UTC date, the next sequence selected from
that date's published tags, and the first eight source commit characters.
Anonymous pull verification, staging, and prod receive the same tag reference.
The existing workflow concurrency group serializes allocation, publication, and
deployment across release runs. The
[tag-reference decision](2026-10-13-tag-image-references.md) supersedes the
original selection of the build digest and owns image-reference selection.
Deployment copies `deploy/docker-compose.yml` as `docker-compose.yml` to the
environment's `DEPLOY_PATH` and runs `docker compose up -d` there. The template
declares `container_name: uniauth`; prod retains that name, and staging renders
`container_name: uniauth-staging`. The Compose projects are `uniauth-prod` and
`uniauth-staging`, with respective SQLite volumes `uniauth-prod_data` and
`uniauth-staging_data`. Distinct container names let operators address both
services directly on the same VM. GitHub owns image publication and file
transfer. The VM owns `.env`, persistent volumes, and Caddy configuration.
The [public GHCR decision](2026-10-13-public-ghcr-images.md)
replaces the original VM registry-login prerequisite and owns image access.
The [deployment reference](../../../../docs/reference/deployment.md) owns the
manual startup command and deployment prerequisites.

## Alternatives

**Publishing after every mainline push** couples each merged change to image
publication and deployment. An explicit operator action controls release timing.

**Rebuilding for prod** creates another artifact after staging used the earlier
build. The original decision chose digest promotion to identify one artifact
for both environments. The current tag-reference decision retains one build and
passes its tag reference to both deployments.

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
