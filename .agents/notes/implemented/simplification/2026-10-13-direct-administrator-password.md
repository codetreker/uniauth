# Agent Note: Direct Deployment Administrator Password

Status: implemented

## Problem

The administrator credential must be configurable alongside the service's other
deployment secrets. Requiring a separately generated encoded credential adds an
operator step and allows an ordinary password to pass configuration loading only
to fail service initialization because it is not a valid encoded hash. Accepted
passwords must also be usable through the administrator Console, and a copied
deployment template must require an operator-supplied credential.

## Decision

`UNIAUTH_ADMIN_PASSWORD` supplies the administrator password directly. Startup
requires a single line containing 1–1024 bytes, without carriage return (CR) or
line feed (LF). The Console compares exact bytes without trimming, so spaces,
dollar signs, and Unicode retain their meaning. Empty, oversized, or multiline
deployment passwords fail startup. The deployment template leaves the variable
empty, requiring an explicit password before the service can start.

The Console uses an HTML password control. The
[HTML standard's password-state rules](https://html.spec.whatwg.org/multipage/input.html#password-state-%28type=password%29)
require that control to remove CR and LF. Rejecting those characters in deployment
configuration keeps every accepted password enterable through the Console.
Login input still requires 1–1024 bytes: invalid-length input returns
`400 invalid_input`, and an exact-byte mismatch returns `401 unauthorized`.

The service has no password-generation command or legacy hash configuration
fallback. The deployment environment holds the credential; SQLite stores neither
administrator passwords nor sessions. Login rate limits, exact Origin and CSRF
checks, and the separate eight-hour in-memory administrator session still apply.
A password change takes effect on restart, which invalidates administrator
sessions, pending product logins, and completed identity results.

This decision partially supersedes the credential encoding and generation step
in the [gateway ownership decision](../architecture/2026-10-12-authentication-gateway.md).
That note retains ownership of administrator authority, product isolation, and
identity handover. The [configuration reference](../../../../docs/reference/configuration.md)
owns environment settings and migration instructions; the
[deployment reference](../../../../docs/reference/deployment.md) owns the VM and
release procedure.

## Alternatives

**Deployment-configured Argon2id hash.** The original gateway accepted a PHC hash
and included a CLI to generate one from a password. It avoided placing the
password itself in runtime configuration, but required operators to generate and
preserve an encoded value before configuring the service. Plain passwords caused
startup format errors. Direct deployment passwords remove that additional format
and preparation step.

## Consequences

Administrators configure one single-line password and use that exact value to
sign in. The password is a deployment secret and is available to processes or
operators with access to the service environment. Single-quoted Compose `.env`
values preserve literal dollar signs. Copying the empty template unchanged fails
startup rather than enabling access with a publicly known placeholder. Operators
replace `UNIAUTH_ADMIN_PASSWORD_HASH` with `UNIAUTH_ADMIN_PASSWORD` and recreate
the container using the updated image. There is no persisted administrator
credential to migrate, and App definitions and product Secret digests remain
unchanged.

Direct comparison removes Argon2id parameter parsing, hash generation, and the
memory-heavy verification concurrency limit. Login rate limits and session
capacity still constrain administrator authentication. Invalid deployment
passwords fail startup immediately through the length and single-line validation.
