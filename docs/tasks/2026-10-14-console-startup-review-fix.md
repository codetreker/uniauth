# Console Startup Credential Review-Fix Receipt

Date: 2026-10-14
Review thread: `4271702725`

## Delivered correction

The `serve` command validates configuration and reserves its TCP listener before
constructing the stateful service. A rejected bind therefore cannot open or
migrate SQLite, change pending bootstrap configuration, reconcile the protected
Console App, or replace its private credential. If service initialization fails,
the command closes the reserved listener and preserves the initialization and
cleanup error chain. Successful construction transfers listener ownership to the
existing serving and shutdown lifecycle.

The [platform decision](../../.agents/notes/implemented/architecture/2026-10-13-public-oauth-platform.md)
owns this ordering and the backend credential lifecycle. Normal restart
provisioning, credential rotation, gateway handover, and the single-instance
deployment contract retain their existing behavior. The
[configuration reference](../reference/configuration.md) describes the observable
startup guarantees. The [original platform receipt](2026-10-13-public-oauth-platform.md)
retains its delivery and browser validation history.

## Observed validation

Before changing production code, the new regressions reproduced the failure on
revision `a5f4d34`: a second command against an occupied address changed the
Console credential and configuration, and the first service's pending OAuth
callback redirected with `login_failed`. Separate fixtures showed a failed bind
creating an absent database and migrating a historical database through the
deliberate schema-3 reset.

The corrected source passed `make build check test`.

| Check | Observed result |
| --- | --- |
| Service Go tests | 543 reported cases, including 119 top-level tests, passed with race detection; total coverage 96.6%. |
| CLI regressions | Full command package race tests passed; coverage 96.8%, with `run` and `Run` at 100%. |
| Coverage policy | Total 90%, package 85%, function 80%, and critical-block requirements passed unchanged, without exclusions. |
| Go SDK | 106 reported cases passed; coverage 99.3%. |
| Console, JavaScript SDK, and release tests | All 60 Console, 16 JavaScript SDK, and 17 existing release tests passed. |
| Package consumers and gateway integration | Packed JavaScript and external Go consumers passed; both real SDK/gateway race tests passed in 1.393 seconds. |
| Static and production checks | Service and SDK builds, formatting, TypeScript checks, Go vet, and diff whitespace checks passed. |
| Independent review | No remaining confirmed in-scope Critical/Major finding. Focused CLI race checks passed in 1.293 seconds; existing server restart, shutdown, and listener-error checks passed in 1.292 seconds. |

The CLI regressions keep a first service live using the production Console and
gateway with controlled providers. Pending login and identity linking complete
after rejected duplicate starts, and a fresh login succeeds. Database snapshots
verify unchanged bootstrap email, App callbacks/providers, private credential,
and migration state. Additional cases verify absent databases stay absent,
historical Apps and Secrets remain intact, failed initialization releases the
address, typed network/SQLite/context causes survive wrapping, and failed starts
emit no success log. Legitimate restart tests still verify private credential
rotation while preserving product credentials and administrator authority.

## Verification limits

Local startup regressions exercise the command entrypoint and real HTTP handlers
with deterministic provider adapters. They do not establish live upstream
registration or consent, deployment, or remote CI success for a later commit.
The original platform receipt owns the prior browser results. Multiple service
instances using different addresses against one database remain outside the
single-instance operating contract. Tests left no service processes or fixture
databases running.
