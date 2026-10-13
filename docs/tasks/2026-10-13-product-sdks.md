# Product SDK Implementation Receipt

Date: 2026-10-13
Base revision: `0cd007b5e8ac0a6718aa92396a5825c9562b5e3f`

## Scope

The Go and JavaScript product SDKs implement the existing authorization,
callback, and one-time identity-retrieval protocol. The standalone Go module uses
the standard library; the Node.js package emits ESM and TypeScript declarations
without runtime dependencies. SDKs preserve full identity source data and raw
response JSON, provide typed errors, and leave browser-bound state, users,
linking, permissions, and sessions with products.

The [SDK reference](../reference/sdks.md) defines the client contract, and the
[SDK decision](../../.agents/notes/implemented/feature/2026-10-13-product-sdks.md)
records its responsibilities and consequences. Existing gateway and Console
validation receipts retain their historical measurements.

## Observed validation

Frozen dependency setup, `make build`, and the final `make check test` run passed.
Service and SDK coverage use the same root policy: total 90%, package 85%,
function 80%, race detection, and required critical-block coverage, with no
package, file, or function exclusions.

| Check | Observed result |
| --- | --- |
| Service Go tests | 276 cases, including 75 top-level tests; 96.7% coverage with race detection. |
| Go SDK tests | 68 cases, including 14 top-level tests; 99.3% coverage with race detection. All functions exceeded 95%. |
| Console tests | All 16 passed. |
| JavaScript SDK tests | All 13 passed, including callback ambiguity, exact JSON, deadlines, transport failures, and redirect rejection. |
| JavaScript package consumer | Actual tarball, bare ESM import, and strict NodeNext declarations with `skipLibCheck: false` passed. Offline installation also passed under `CI=true`. |
| Go package consumer | An external module imported the SDK with `GOWORK=off`; its module graph contains only the consumer and SDK. |
| Gateway integration | Both Go and JavaScript integration tests passed with race detection in 1.248 seconds. |
| Browser scenarios | All three existing Console and desktop/mobile mockup scenarios passed against the freshly built executable in 14.3 seconds. |
| Static and document checks | Formatting, service/SDK/fixture vet, TypeScript, Prettier, and checks for 26 active documents passed. |
| Independent review | Go and JavaScript reviewers found no remaining Critical or Major issues. |
| Cleanup | Probe servers, browser services, temporary consumers, tarballs, and SQLite fixtures were removed. |

Integration tests use the real authentication handler, SQLite App registry,
browser-bound transaction, and memory result cache with a controlled provider.
Wrong Secrets return 401 and wrong-App claims return 410 without consuming the
rightful App's result. A valid retrieval preserves unknown fields, nested data,
nulls, false values, and the raw integer `9007199254740993`. Replays return 410;
twelve concurrent valid claims yield one success. Go also checks the exact
five-minute expiry boundary.

Independent transport probes observed one received POST when the server consumed
it and dropped the response. Redirect handling did not mutate the caller's Go
client. Context, timeout, network, and body-read causes remained observable.
Both clients accepted complete identity responses larger than 8 MiB. JavaScript
retained the exact raw JSON while documenting native numeric precision limits.

## Verification limits

The delivery does not publish npm packages, Go tags, or registry releases. Local
packed/imported consumers verify artifact contents and usability, not remote
publication. Interactive Microsoft, Google, and GitHub login with deployed
credentials is not established by SDK tests. These local measurements do not
claim remote CI success for a revision whose checks have not completed.

Products atomically validate and consume browser-bound pending state before the
SDK's one-time result request. SDKs do not persist that state or create sessions.
A failed, aborted, or malformed response may follow server-side consumption and
requires a fresh login; the clients do not retry the same result code.
