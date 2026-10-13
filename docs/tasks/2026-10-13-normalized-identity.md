# Normalized Identity Validation Receipt

- Date: 2026-10-13
- Base revision: `0cd007b`
- Branch: `feat/normalized-identity`

## Delivered behavior

Microsoft, Google, and GitHub adapters produce the seven-field identity described
in the [API reference](../reference/api.md): `provider`, `id`, `email`,
`email_verified`, `name`, `avatar_url`, and `origin`. Qualified identity values
remain unchanged. Optional standard fields are nullable, and explicit
verification values retain their meaning. Complete obtained source data remains
in `origin`; malformed known profile fields fail with the original JSON cause.

The [normalization decision](../../.agents/notes/implemented/architecture/2026-10-13-normalized-identity.md)
records provider selection rules, the wire-format change, and Microsoft photo
representation costs. App binding, five-minute expiry, serialization before
publication, capacity accounting, and atomic single consumption retain their
existing behavior.

## Observed validation

| Check | Result |
| --- | --- |
| Focused provider, authentication, and server race tests | Passed with repository-local caches and bounded deadlines. |
| `make build check test` | Passed: production build, document links and note lifecycle, whitespace and Go formatting, `go vet`, TypeScript, Prettier, go-cov, and frontend tests. |
| Authoritative go-cov gate | Passed with race detection: 316 tests including subtests, 79 top-level tests; total coverage 96.8%, providers 95.7%, authentication 100.0%. |
| Frontend unit tests | 16 passed across three files. |
| `pnpm test:browser` with repository-local Chromium | Three passed: service-backed Console management, desktop preview, and mobile preview. |
| Independent code and handover/document reviews | No critical or major findings, and no substantive minor follow-up. |

Provider fixtures cover populated, absent, null, empty, and false attributes;
canonical source selection and precedence; exact email verification matching;
a primary email on a later page; precise numeric IDs above JavaScript's safe
integer range; Microsoft photo data URLs and absence; malformed field types;
complete original data; and credential exclusion. HTTP handover tests assert
the seven keys, nullable fields, preservation of populated fields and nested
origin data, required identity data, and once-only retrieval.

The task's service and browser processes stopped after validation. Temporary
browser binaries were removed from the worktree.

## Validation limits

Provider tests use local OAuth/OIDC and identity endpoint fixtures. They do not
establish production provider registration, credentials, interactive consent,
or live profile contents. Browser tests exercise Console and preview behavior;
they do not perform upstream account login. Those checks require configured
provider registrations and deployment validation.
