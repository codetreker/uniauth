# Docker Compose Releases

[Release](../../.github/workflows/release.yml) runs on pushes to `main` and manual
runs on `main`. Build publishes `REGISTRY/default/uniauth:sha-COMMIT` for Linux
AMD64 and ARM64. Staging and prod use the same image digest.

Each deployment sets the image and environment name in
[deploy/docker-compose.yml](../../deploy/docker-compose.yml), copies that file
to `DEPLOY_PATH` as `docker-compose.yml`, and runs `cd "$DEPLOY_PATH" && docker compose up -d`. Prod starts after staging's
command succeeds. Startup success does not establish application readiness.
GitHub Environment protection rules can require approval for prod.

## GitHub settings

| Setting | Kind | Purpose |
| --- | --- | --- |
| `REGISTRY` | Organization/repository variable | Harbor host; the `default` project must exist. |
| `HARBOR_USER` | Organization/repository variable | Image publishing account. |
| `HARBOR_API_TOKEN` | Secret | Image publishing token. |
| `OC_VM_HOST` | Organization/repository variable | VM SSH host. |
| `OC_VM_USER` | Organization/repository variable | VM SSH user. |
| `OC_VM_SSH_KEY` | Secret | VM SSH private key. |
| `DEPLOY_PATH` | `staging` / `prod` Environment variable | Deployment directory on the VM. |

Set `DEPLOY_PATH` separately in the `staging` and `prod` GitHub Environments.
The two values must point to different VM directories.

## VM configuration

Prepare a separate deployment directory for each environment. The SSH user needs
write access to these directories and permission to run Docker Compose. Docker
must already be logged into the Registry with pull access to `default/uniauth`.
Docker Compose and tar must be installed.

Place an independently configured `.env` in each directory using
[the template](../../deploy/.env.example). Set `UNIAUTH_HOST_PORT` to distinct
loopback ports and configure the service's public URL, administrator password hash,
and provider credential pairs. Use single quotes around values containing dollar
signs, including the Argon2id password hash, to preserve them during Compose parsing.
See [runtime configuration](configuration.md) for the complete service settings.

The workflow replaces only `docker-compose.yml`; `.env` remains VM-owned. Caddy
configuration is operator-owned. The service listens on
`127.0.0.1:UNIAUTH_HOST_PORT`, and SQLite persists in separate
`uniauth-staging_data` and `uniauth-prod_data` volumes. Service restarts invalidate
pending logins, results, and administrator sessions.

The [release decision](../../.agents/notes/implemented/process/2026-10-13-compose-releases.md)
records deployment ownership. The [validation receipt](../tasks/2026-10-13-compose-releases.md)
records observed checks and verification limits.
