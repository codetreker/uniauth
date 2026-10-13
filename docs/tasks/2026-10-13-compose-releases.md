# Docker Compose Release Validation

Date: 2026-10-13

## Delivered behavior

Build publishes one image. Staging and prod copy their configured
`deploy/docker-compose.yml` into `DEPLOY_PATH` as `docker-compose.yml` and run
`docker compose up -d`.
VM-owned `.env` files and separate Compose project volumes retain configuration.
[Public GHCR publication](../reference/deployment.md) supersedes the original
registry-login prerequisite; this receipt retains the Compose delivery checks.

## Observed validation

| Check | Result |
| --- | --- |
| Native Docker build | Passed for AMD64; the retained Dockerfile also passed ARM64 cross-compilation. |
| Local Compose startup | Both environments started with `up -d`; administrator login and literal dollar signs in password/OAuth credentials passed. Loopback ports and separate volumes were confirmed, and `.env` stayed unchanged. |
| `make check` and actionlint | Formatting, vet, frontend types, active links, skill metadata, note lifecycle, discovery symlinks, workflow definitions, and diff whitespace passed. |
| Independent review | No confirmed critical or major issue. |

Local fixture containers, volumes, networks, and files were removed.

## Verification limits

No Registry publication or OC VM deployment was performed. `.env`, deployment
paths, and Caddy routing require operator configuration.
The detached Compose command does not establish application readiness.
