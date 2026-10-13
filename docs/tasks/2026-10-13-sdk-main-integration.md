# SDK and Main Integration Receipt

Date: 2026-10-13
Main integration revision: `5592e0d4e72a822dd6aa1284ba5a0a95013a08c5`

## Scope

The SDK branch incorporates the current mainline normalized identity contract
and release documentation. Three documentation conflicts are resolved with both
the Go/JavaScript product SDK contract and incoming deployment/public GHCR
requirements preserved.

The clients expose the seven mandatory server fields: `provider`, `id`,
`email`, `email_verified`, `name`, `avatar_url`, and `origin`. Nullable profile
values remain nullable, and explicit verification `false` remains `false`.
Missing fields and wrong types are protocol errors. Go uses `ID`, nullable scalar
pointers, and raw `Origin`; JavaScript uses the wire's snake-case field names.
Both retain whole-response raw JSON. SDKs neither derive profile values nor add
compatibility aliases for the earlier, unreleased client shape.

Authorization URLs, callback state validation, product-owned atomic pending-state
consumption, credentials, one POST without redirects or retries, and the
five-minute single-use server result contract remain unchanged. The
[normalized identity decision](../../.agents/notes/implemented/architecture/2026-10-13-normalized-identity.md)
owns server mapping, while the
[SDK decision](../../.agents/notes/implemented/feature/2026-10-13-product-sdks.md)
owns package behavior. The [SDK reference](../reference/sdks.md) defines current
consumer types. Earlier task receipts preserve their original validation results.

## Observed validation

The fresh `make build check test` run passed for the integrated implementation.

| Check | Observed result |
| --- | --- |
| Production build | Console, both SDKs, and Go executable passed. |
| Static and documentation checks | Go formatting and vet, TypeScript, Prettier, diff whitespace, and all 34 active-document/link/lifecycle checks passed. |
| Service Go tests | 316 cases, including 79 top-level tests, passed with race detection; coverage was 96.8%. |
| Go SDK tests | 106 cases, including 16 top-level tests, passed with race detection; coverage was 99.3%. Eighteen functions reached 100%; `Retrieve` reached 96.2%. |
| Coverage policy | Shared total 90%, package 85%, function 80%, and critical-block requirements passed without exclusions for both Go modules. |
| JavaScript SDK tests | All 16 passed. |
| Console tests | All 16 passed. |
| Packed JavaScript consumer | Bare ESM tarball import and strict NodeNext declaration checks passed with the normalized identity fields. |
| External Go consumer | Passed with standard-library dependencies only. |
| Real SDK/gateway integration | Both race-enabled tests passed in 1.352 seconds in the final parent run. |
| Independent review | Go and JavaScript reviews found no critical or major issues. |
| Browser scenarios | All three passed against a fresh executable in 14.7 seconds: real Console 4.9 seconds, desktop mockup 4.4 seconds, and mobile mockup 1.6 seconds. |
| Probe cleanup | Independent review probes and their temporary resources were cleaned up. |

The integration uses the actual authentication handler, SQLite App registry,
browser-bound login, and in-memory result cache. It verifies all seven normalized
fields, null values, explicit verification `false`, and complete raw `origin`
data. Wrong Secrets and wrong-App probes leave results claimable; replay fails;
twelve concurrent valid claims yield one success; the expiry boundary is
preserved. Both client artifacts use the current wire format.

## Deferred review follow-ups

The Go decoder's handling of unknown top-level keys that are case-equivalent to
canonical struct fields remains a minor follow-up. The current gateway emits
only the seven canonical fields, so this does not affect the reviewed server
responses. The synthetic integration fixture's unqualified ID can be strengthened
separately; independent SDK unit tests cover qualified identity values. Neither
follow-up expands this integration's production behavior.

## Verification limits

These local results do not establish a registry package publication, Go release
tag, live third-party login, deployed release success, or remote CI success for
a revision that has not completed its checks. Deployment and public GHCR
requirements remain owned by their existing references and decision notes.
No compatibility path preserves the previous SDK shape, which was not merged or
published before this integration.
