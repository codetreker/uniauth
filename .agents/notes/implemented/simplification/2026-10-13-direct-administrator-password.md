# Agent Note: Direct Deployment Administrator Password

Status: implemented

## Problem

The original administrator credential needed to be configurable alongside other
deployment secrets. A separately generated encoded credential added an operator
step and allowed an ordinary password to pass configuration loading before
failing service initialization because it was not a valid encoded hash.
Accepted passwords also needed to work through the Console's password control.

## Decision

The direct-password delivery replaced the deployment-configured Argon2id hash
with `UNIAUTH_ADMIN_PASSWORD`, compared as exact bytes without trimming. Its
startup contract accepted a single line containing 1–1024 bytes, rejected empty,
oversized, and multiline values, and preserved spaces, dollar signs, and Unicode.
The deployment template left the value empty to require operator configuration.

The [HTML standard's password-state rules](https://html.spec.whatwg.org/multipage/input.html#password-state-%28type=password%29)
remove carriage return and line feed from that control. Rejecting those characters
at configuration time kept every accepted password enterable. That implementation
had no hash-generation command or legacy hash fallback and held no persisted
administrator password or session in SQLite. Exact Origin, CSRF, rate limits,
and an eight-hour memory session constrained its management interface.

This choice superseded only credential encoding and generation in the
[gateway ownership decision](../architecture/2026-10-12-authentication-gateway.md).
The [public OAuth platform decision](../architecture/2026-10-13-public-oauth-platform.md)
now supersedes password authentication itself. Current Console login is OAuth-only;
password variables and inputs are removed. This note preserves the reasons for
removing the earlier format step, without describing a supported login mechanism.
The [configuration](../../../../docs/reference/configuration.md) and
[deployment](../../../../docs/reference/deployment.md) references describe current
bootstrap and operations.

## Alternatives

**Deployment-configured Argon2id hash.** The original gateway accepted a PHC hash
and provided a generation CLI. It avoided placing the password itself in runtime
configuration, but required preserving an encoded value before startup. Plain
passwords caused format errors. The direct-password decision removed that
additional format and preparation step.

## Consequences

The historical delivery let operators configure one exact single-line password.
That value was available to processes or operators with environment access;
single-quoted Compose values preserved dollar signs. Copying an empty template
failed startup instead of enabling a known placeholder. Operators replaced the
hash variable with the direct password and recreated their container. There was
no persisted administrator credential to migrate, and that change preserved App
definitions and product Secret digests.

Direct comparison removed Argon2id parsing, generation, and memory-heavy
verification concurrency limits. The later public platform removes shared
password authority and deliberately resets pre-platform Apps and Secrets; its
new account ownership, bootstrap trust, and reset costs belong to the linked
platform decision.
