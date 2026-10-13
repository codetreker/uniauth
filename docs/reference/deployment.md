# Docker Compose Releases

[Release](../../.github/workflows/release.yml) runs on pushes to `main` and manual
runs on `main`. Its jobs are `build → staging → prod`. Build runs repository
checks, coverage/race tests, browser scenarios, and container tests before
publishing `REGISTRY/default/uniauth:sha-COMMIT`. The image contains the Go service
and production Console assets and supports Linux AMD64 and ARM64.

Both deployment jobs receive the same immutable `image@sha256:...` reference.
Staging must pass the container's `/readyz` healthcheck before prod starts. The
GitHub Environments are named `staging` and `prod`; configure required reviewers
on `prod` when approval is required. Referencing an environment alone does not
enable approval. Whole release runs are serialized, with running releases retained.

## GitHub settings

Organization variables must grant this repository access. Registry settings must
also be available to the build job, which has no environment binding.

| Setting | Kind | Purpose |
| --- | --- | --- |
| `REGISTRY` | Variable | Harbor host, optionally including a port; no scheme or path. The Harbor project `default` must exist. |
| `HARBOR_USER` | Variable | Registry account with push/pull access to `default/uniauth`. |
| `HARBOR_API_TOKEN` | Secret | Registry account token. |
| `OC_VM_HOST` | Variable | VM DNS name or IPv4 address; SSH uses port 22. |
| `OC_VM_USER` | Variable | VM deployment user. |
| `OC_VM_SSH_KEY` | Secret | Private SSH key for that user. |
| `OC_VM_KNOWN_HOSTS` | Variable | Trusted OpenSSH `known_hosts` entry for the VM, verified through the VM administrator. |
| `DEPLOY_PATH` | `staging` / `prod` Environment variable | Absolute deployment directory on the VM; use a distinct directory for each environment. |
| `UNIAUTH_HOST_PORT` | `staging` / `prod` Environment variable | Distinct loopback host port for each environment, for example `18081` and `18080`. |

Create both GitHub Environments and configure their paths, ports, and any required
protection rules before release. The workflow has no hardcoded VM deployment paths.

## Prepare the VM

The VM needs Docker Engine, Docker Compose 2.30 or later, Bash, tar, flock, and
GNU timeout. The SSH user needs Docker daemon access and write access to each
configured `DEPLOY_PATH`. Releases require no interactive sudo.

Create both deployment directories. Place an independently configured `.env`
in each, using [the template](../../deploy/.env.example). Generate the administrator
password hash with the built executable or the image's `hash-password` command:

```sh
docker run --rm -it REGISTRY/default/uniauth@sha256:DIGEST hash-password
```

Set each environment's `UNIAUTH_PUBLIC_URL`, administrator hash, and any provider
Client ID / Client Secret pairs in `.env`. See [runtime configuration](configuration.md)
for validation and exact upstream callback URLs. The operator owns these files
and Caddy's domains/TLS routing; releases preserve `.env`.

Use restrictive file permissions, such as `chmod 600 .env`, with the file readable
by the deployment user. Compose loads `.env` with `format: raw`: copy password
hashes and credentials literally without surrounding shell quotes. Dollar signs
are preserved. The workflow's explicit `--env-file release.env` keeps runtime
credentials separate from Compose interpolation. Image and host-port settings
belong in the managed `release.env`.

Compose fixes the listener to `:8080` and database path to `/data/uniauth.db`.
The volume is initialized for UID/GID `10001:10001`. Environment identity is fixed
by the Compose project, independently of its deployment directory:

| Environment | Compose project | Persistent volume |
| --- | --- | --- |
| staging | `uniauth-staging` | `uniauth-staging_data` |
| prod | `uniauth-prod` | `uniauth-prod_data` |

The service publishes only `127.0.0.1:UNIAUTH_HOST_PORT`. Route each origin through
Caddy to its matching host listener. A containerized proxy requires a configured
route to that host listener. The workflow does not manage proxy configuration.

## Deployment and failure behavior

The SSH client verifies the configured host key. It streams Compose configuration,
image/port settings, and registry credentials into a restricted temporary directory
inside `DEPLOY_PATH`. Registry login uses a temporary Docker config. Normal
completion and handled signals remove release credentials; forced SIGKILL can
leave restricted `.uniauth-release.*` directories. Remove such directories only
after confirming the corresponding release processes have exited.

A lock inside the environment's deployment directory serializes its changes.
An eight-minute VM deadline bounds the deployment process group. Signal cleanup
also terminates children that ignore TERM and inherit the lock; the client SSH
deadline is nine minutes. The workflow validates Compose and pulls the image
before replacing `compose.yml` and `release.env`.

`docker compose up --wait --wait-timeout 120` recreates the single service and
waits for readiness. Pull, configuration, startup, or readiness failures fail the
job and block production promotion. Logs rotate at 10 MiB with three files, and
30 seconds of stop grace permits the service's 15-second drain. Volumes persist.

Restart invalidates pending logins, one-time results, and Console sessions;
products start fresh logins. Readiness verifies SQLite access. Public Caddy routing
and upstream provider logins require verification with the configured endpoints
and credentials.

A failed replacement can leave the environment unavailable. Automatic code or
SQLite migration rollback is not performed. For recovery, select a known image
digest compatible with the stored schema, replace `UNIAUTH_IMAGE` in `release.env`,
and run from that environment's `DEPLOY_PATH`:

```sh
docker compose --project-name uniauth-prod --env-file release.env --file compose.yml up --detach --wait --wait-timeout 120
```

Use `uniauth-staging` for staging. Preserve the volume and use a SQLite-aware
backup before changes requiring schema recovery.

## Validation

`make test-release` builds a local image and runs isolated Compose projects. It
checks readiness, embedded Console assets, literal password-hash handling,
administrator login, App/credential persistence after restart, session invalidation,
and staging/prod isolation. Containers, volumes, and temporary files are removed
on exit. Both Check and Release run this validation before accepting the image.

The [release decision](../../.agents/notes/implemented/process/2026-10-13-compose-releases.md)
records promotion and state-management choices. The
[validation receipt](../tasks/2026-10-13-compose-releases.md) records observed checks
and external verification limits.
