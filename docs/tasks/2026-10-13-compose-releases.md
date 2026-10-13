# Docker Compose Release Validation

Date: 2026-10-13

## Delivered behavior

The release workflow builds one Linux AMD64/ARM64 image and promotes its immutable
digest through staging and prod. Environment-specific `DEPLOY_PATH` variables,
VM-local `.env` files, and separate Compose volumes isolate credentials and
durable application configuration. Container readiness gates promotion;
bounded remote execution and signal cleanup release
temporary registry credentials and deployment locks.

The [deployment reference](../reference/deployment.md) owns configuration,
prerequisites, recovery, and verification limits. The
[release decision](../../.agents/notes/implemented/process/2026-10-13-compose-releases.md)
records alternatives and operational consequences.

## Observed validation

| Check | Result |
| --- | --- |
| `make setup` | Passed using the pinned Go and pnpm dependencies. |
| `make build check test` | Passed source build, documentation/skill/symlink checks, formatting, vet, TypeScript, ShellCheck, coverage/race policy, and Console unit tests. Go total coverage was 96.7%; all 16 frontend tests passed. |
| Playwright browser suite | All three service/Console and desktop/mobile preview scenarios passed in 14.7 seconds using a repository-local temporary Chromium install. |
| Native Docker build | Passed for Linux AMD64; service commands, non-root UID/GID, writable data directory, and CA roots checked. |
| ARM64 cross-compilation | Passed; target binary inspected as a statically linked ARM aarch64 executable. |
| Actual Compose smoke | Passed fresh-volume readiness, embedded Console/assets, literal Argon2id hash login, App/Secret creation, restart session invalidation, retained App/credential authentication, and prod isolation. |
| `make test-release` | Passed after switching runtime files to `.env`; both projects, volumes, networks, and fixtures were removed. |
| Smoke failure cleanup | Injected verifier failure retained exit 42 and removed both environments' containers, volumes, networks, and temporary files. Missing image argument returned exit 2. |
| Shell syntax and ShellCheck | Passed for deployment and smoke scripts. |
| actionlint 1.7.12 | Passed all workflow definitions. |
| Deployment transport and lifecycle probes | Both environments passed configured paths containing spaces, apostrophes, and dollar signs; raw `.env` values remained literal. Missing files and login/pull/up failures propagated. Timeout and TERM-resistant descendant probes released locks, removed credentials, and preserved the independent SSH caller group. |

Independent packaging review found no critical or major issue. Deployment review
identified an inherited-lock lifecycle issue with an unbounded remote Docker
child; the deployment wrapper now supervises the complete VM process group under
an eight-minute deadline. Signal handling removes credentials and terminates the
whole group, including TERM-resistant children. Transport probes preserved success
and original login/pull/replacement failure codes and verified inherited-lock
release, literal credential quoting, and isolation from the SSH caller's group.
Final independent review found no remaining critical or major issue. Forced
SIGKILL during stalled extraction can leave restricted temporary directories;
the deployment reference documents their operator cleanup.

## Verification limits

No Registry publication, OC VM connection, or staging/prod deployment was performed.
The organization settings API returned HTTP 403, so repository access to the
configured organization variables was not verified. The VM's `.env` files and
Caddy configuration remain operator-owned prerequisites.

Full ARM64 runtime execution, environment protection rules, HTTPS/reverse-proxy
routing, and interactive Microsoft/Google/GitHub login require configured external
environments. Readiness and fixture checks do not establish those behaviors.
