# Docker Compose Releases

[Release](../../.github/workflows/release.yml) starts only through manual
`workflow_dispatch`. Select `main` when starting it from GitHub Actions, or run:

```sh
gh workflow run release.yml --ref main
```

The build job requires `refs/heads/main`; selecting another ref skips the build
and dependent deployments. Pushes to `main` do not start a release.

Build publishes `ghcr.io/codetreker/uniauth:sha-COMMIT16`, where `COMMIT16` is the
first 16 characters of the source commit hash, for Linux AMD64 and ARM64 using
`GITHUB_TOKEN` with `packages: write` permission. A separate Docker config
verifies anonymous pull before deployment. Staging and prod use the same image
digest; deployment selects the built artifact by digest rather than by tag.

Each deployment sets the image and environment name in
[deploy/docker-compose.yml](../../deploy/docker-compose.yml), copies that file
to `DEPLOY_PATH` as `docker-compose.yml`, and runs
`cd "$DEPLOY_PATH" && docker compose up -d`. Prod starts after staging's command
succeeds. Startup success does not establish application readiness.
GitHub Environment protection rules can require approval for prod.

## Public GHCR package

GitHub creates a new container package as private, even when its linked repository
is public. After the first push, open
[UniAuth Package settings](https://github.com/orgs/codetreker/packages/container/uniauth/settings)
and select **Danger Zone → Change visibility → Public**. This is a one-time package
setting; subsequent versions keep the package's visibility. GitHub provides no
supported REST or GraphQL visibility-setting operation for the workflow.
[GitHub's GHCR and visibility documentation](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
defines initial visibility, and
[package access-control documentation](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)
distinguishes repository access inheritance from public visibility.

If the first release's anonymous pull fails because the package is still private,
change that setting to Public before starting another release. The failed build
job prevents staging and prod from starting with a private image.

## GitHub settings

| Setting | Kind | Purpose |
| --- | --- | --- |
| `GITHUB_TOKEN` | Automatically provided token | Publishes the repository's GHCR package. |
| `OC_VM_HOST` | Organization/repository variable | VM SSH host. |
| `OC_VM_USER` | Organization/repository variable | VM SSH user. |
| `OC_VM_SSH_KEY` | Secret | VM SSH private key. |
| `DEPLOY_PATH` | `staging` / `prod` Environment variable | Deployment directory on the VM. |

Set `DEPLOY_PATH` separately in the `staging` and `prod` GitHub Environments.
The two values must point to different VM directories. Image publication uses
GitHub-managed credentials and does not require Registry or Harbor settings.

## VM configuration

Prepare a separate deployment directory for each environment. The SSH user needs
write access to these directories and permission to run Docker Compose.
Docker Compose and tar must be installed. Public GHCR images are pulled anonymously.

Place an independently configured `.env` in each directory using
[the template](../../deploy/.env.example). Set `UNIAUTH_HOST_PORT` to distinct
loopback ports and configure the service's public URL, `UNIAUTH_ADMIN_PASSWORD`,
and provider credential pairs. Supply the actual administrator login password in
the deployment environment. Use single quotes around `.env` values containing
dollar signs to preserve them during Compose parsing.
See [runtime configuration](configuration.md) for the complete service settings.

The workflow replaces only `docker-compose.yml`; `.env` remains VM-owned. Caddy
configuration is operator-owned. The service listens on
`127.0.0.1:UNIAUTH_HOST_PORT`, and SQLite persists in separate
`uniauth-staging_data` and `uniauth-prod_data` volumes. Service restarts invalidate
pending logins, results, and administrator sessions.

The [Compose release decision](../../.agents/notes/implemented/process/2026-10-13-compose-releases.md)
records deployment ownership, and the
[public GHCR decision](../../.agents/notes/implemented/process/2026-10-13-public-ghcr-images.md)
records image access. The [Compose validation receipt](../tasks/2026-10-13-compose-releases.md)
and [GHCR validation receipt](../tasks/2026-10-13-public-ghcr-images.md) distinguish
observed checks from external setup.
