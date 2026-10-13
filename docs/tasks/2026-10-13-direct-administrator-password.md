# Direct Administrator Password Validation

Date: 2026-10-13

## Delivered behavior

`UNIAUTH_ADMIN_PASSWORD` supplies the Console administrator password directly.
Startup and login require 1–1024 bytes, and comparison preserves exact password
bytes, including whitespace and dollar signs. The password-generation command,
Argon2id dependency, hash configuration, and verification concurrency control are
removed. Login rate limits, administrator sessions, Origin and CSRF checks, and
product credentials retain their existing behavior.

The [administrator password decision](../../.agents/notes/implemented/simplification/2026-10-13-direct-administrator-password.md)
records the choice and its deployment costs. The
[configuration reference](../reference/configuration.md) defines the new variable
and the existing-deployment migration. Earlier task receipts retain their original
validation results.

## Observed validation

The final `make build check test` run passed for the direct-password implementation.

| Check | Observed result |
| --- | --- |
| Production build and static checks | Console assets, both SDKs, and the Go executable built; Go formatting and vet, TypeScript, Prettier, and diff whitespace checks passed. |
| Service Go tests | 305 cases, including 75 top-level tests, passed with race detection; total coverage was 97.0%, and Console package coverage was 99.3%. |
| Go SDK tests | 106 cases, including 16 top-level tests, passed with race detection; coverage was 99.3%. |
| Coverage policy | Unchanged total 90%, package 85%, function 80%, and critical-block requirements passed without exclusions for both Go modules. |
| Console and JavaScript SDK tests | All 16 tests passed in each suite. |
| Package consumers | Packed JavaScript runtime/declaration checks and the external Go consumer passed. |
| Real SDK/gateway integration | Both race-enabled tests passed in 1.259 seconds. |
| Browser scenarios | All three passed against a fresh executable in 14.5 seconds: Console 4.1 seconds, desktop mockup 4.6 seconds, and mobile mockup 1.5 seconds. |
| Production Dockerfile | Build passed; local image ID began with `8b18d5ed2d41`. |
| Compose runtime | The production image ran as UID/GID `10001:10001`; dollar signs, Unicode, and surrounding spaces arrived as the exact configured password. A trimmed password returned `401`, the exact password returned `200`, and its session returned `200`. |
| Password rotation | Recreating the container with a changed password invalidated the old session (`401`) and old password (`401`); the new password returned `200`. |
| Independent review | No confirmed critical or major issue and no substantive minor follow-up. |
| Documentation | All 36 active-document, link, skill metadata, note lifecycle, and discovery-alias checks passed. |
| Cleanup | Fixture containers, networks, and volumes were removed; removal was verified. |

Independent probes accepted one-byte and 1024-byte passwords and rejected
1025-byte and 1026-byte values. Legacy hash-only configuration failed startup.
Unicode and CRLF password input matched only when its bytes were exact. With a
fixed clock, 32 concurrent login attempts produced five accepted attempts and
27 rate-limited responses. Session behavior, CSRF, logout, and log redaction
passed. The production Compose probe found no passwords in service logs.

## Verification limits

Local checks establish executable, package, browser, and production-container
behavior with synthetic credentials. They do not establish remote CI success,
image publication, release completion, deployment to the OC VM, or live
third-party provider login for this change. Deployment settings and the existing
release procedure remain operator-owned.
