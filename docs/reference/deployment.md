# Docker Compose Releases

[Release](../../.github/workflows/release.yml) starts only through manual
`workflow_dispatch`. Select `main` when starting it from GitHub Actions, or run:

```sh
gh workflow run release.yml --ref main
```

The build job requires `refs/heads/main`; selecting another ref skips the build
and dependent deployments. Pushes to `main` do not start a release.

Each Actions run title has the form
`Release #<run-number> · staging → prod · <branch> · @<actor> · <commit>`.
The number is GitHub's workflow run number, the actor is the original initiator,
and the commit is the full Git commit hash from `github.sha`. Rerunning an
existing run retains those values. The daily image-tag sequence is allocated
separately by the build job, whose tag uses the commit's first eight characters.

## Release image reference

The build job allocates a tag in `<yymmdd>-<seq>-<hash-8>` format and derives
one full reference, such as `ghcr.io/codetreker/uniauth:261013-3-a1b2c3d4`.
`yymmdd` is the UTC date captured once when the allocation step runs, and
`hash-8` is the first eight characters of the source commit hash. The date is
not the date when the workflow was queued or created.

The allocator queries all published registry tags, following every pagination
page, with authenticated GitHub actor and `GITHUB_TOKEN` credentials. For tags
matching the current date and complete format, `seq` is the largest numeric
sequence plus one; without a match it is `1`. Older `sha-` tags do not
participate. Registry authentication, network, and malformed-response failures
stop the build. The existing workflow concurrency group serializes releases
through image publication and deployment.

Sequences track currently published tags. A failure before publication can
leave its sequence available for a later attempt. A published tag contributes
to subsequent allocation even if deployment fails. Rerunning the full build
allocates the next published sequence, including when rebuilding the same
commit. A new UTC date starts at `1` when it has no matching published tags.

The workflow publishes the allocated tag for Linux AMD64 and ARM64 using
`GITHUB_TOKEN` with `packages: write` permission. A separate Docker config
verifies anonymous pull of that same reference before deployment. The build
job's `image` output passes it unchanged to staging and prod. Tags are mutable;
matching references do not themselves establish identical content across pulls.
The [tag-reference decision](../../.agents/notes/implemented/process/2026-10-13-tag-image-references.md)
owns allocation, image selection, and their consequences.

## Deployment

Each deployment sets the image and environment name in
[deploy/docker-compose.yml](../../deploy/docker-compose.yml), copies that file
to `DEPLOY_PATH` as `docker-compose.yml`, and runs
`cd "$DEPLOY_PATH" && docker compose up -d`. Prod starts after staging's command
succeeds. Startup success does not establish application readiness.
GitHub Environment protection rules can require approval for prod.

The Compose template declares `container_name: uniauth`, which prod retains.
Staging renders that line as `container_name: uniauth-staging`. Each environment
has its own container name, Compose project, and persistent data volume for
SQLite and rotating JSON logs:

| Environment | Container name | Compose project | Data volume |
| --- | --- | --- | --- |
| staging | `uniauth-staging` | `uniauth-staging` | `uniauth-staging_data` |
| prod | `uniauth` | `uniauth-prod` | `uniauth-prod_data` |

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
| `GITHUB_TOKEN` | Automatically provided token | Authenticates registry tag allocation and publishes the repository's GHCR package. |
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
loopback ports and configure the public URL, `UNIAUTH_INITIAL_ADMIN_EMAIL`,
and at least one provider credential pair. The template leaves the initial email
empty; supply a plain address such as `sychenjianjun@gmail.com` before startup.
The Console uses OAuth only. Google or GitHub verified email evidence can claim
initial administrator authority; Microsoft alone cannot claim it. Changing the
configured email after that one-time claim does not transfer authority.

Migration 3 clears all existing Apps and Secrets when upgrading to the public
platform schema. Recreate the product registrations and replace their backend
credentials after the upgrade. The final schema then persists Console accounts,
linked identities, owned Apps, and credential digests across restarts. See [runtime configuration](configuration.md) for the complete service settings.

The workflow replaces only `docker-compose.yml`; `.env` remains VM-owned. Caddy
configuration is operator-owned. The service listens on
`127.0.0.1:UNIAUTH_HOST_PORT`. SQLite and the default log files persist in the
environment's named volume. Service restarts invalidate pending logins, results,
Console login/link flows, and Console sessions.

## Diagnostic log files

Compose sets `UNIAUTH_LOG_FILE` to `/data/uniauth.log` when the VM `.env` does not
supply the variable. Existing deployments gain persistent logs when the updated
Compose file and application are deployed. An explicit empty value disables the
file while retaining stderr output. A custom path must have an existing writable
parent directory and a volume mount if it must survive container replacement.
The image runs as UID/GID `10001:10001`; its `/data` volume is writable by that
identity.

The application appends JSON records alongside stderr and synchronously rotates
at 10 MiB, retaining `/data/uniauth.log.1`, `.2`, and `.3` with `.1` newest.
New files are private (`0600`). Startup fails if the configured file cannot be
opened; write or rotation failures stop the service with a diagnostic. Configure
one log path per process. Each staging/prod volume keeps its own log history.

After reproducing a login failure, copy the active log from prod to the VM:

```sh
docker cp uniauth:/data/uniauth.log ./uniauth.log
```

Use `uniauth-staging` for staging. The three backup paths can be copied the same
way when the relevant request spans a rotation. `docker logs uniauth` continues
to show the same JSON records. Filter provider records by `component=provider`
and group them with the authentication/HTTP records using `operation_id` and
`request_id`. The [configuration reference](configuration.md#logs-and-readiness)
defines stage timings, remaining deadlines, transport observations, and excluded
data. These diagnostics explain real authenticated provider work; a fast
unauthenticated Graph `401` does not establish authenticated profile performance.

Fully verified Microsoft logins can succeed with unavailable Graph enrichment.
Filter `stage=profile_enrichment` records with `outcome=partial` to find their
`unavailable_sources`, then inspect correlated per-stage failures. Profile and
photo share a fixed 3-second budget after the 20-second core authentication
budget; caller cancellation and the 30-second callback deadline remain fatal.
A photo `404` alone is confirmed absence and emits no partial warning. The
result omits failed or unattempted source keys, retains complete accepted sources,
and keeps every normalized field present with nullable attributes. Collection,
rotation, and file-write failure behavior are unchanged.

The [Compose release decision](../../.agents/notes/implemented/process/2026-10-13-compose-releases.md)
records manual initiation and deployment ownership, and the
[public GHCR decision](../../.agents/notes/implemented/process/2026-10-13-public-ghcr-images.md)
records image access. The [Compose validation receipt](../tasks/2026-10-13-compose-releases.md)
and [GHCR validation receipt](../tasks/2026-10-13-public-ghcr-images.md) distinguish
observed checks from external setup.
