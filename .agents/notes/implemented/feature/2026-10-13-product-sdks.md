# Agent Note: Product Backend SDKs

Status: implemented

## Problem

Product backends need to construct the same UniAuth authorization request, reject
ambiguous callbacks, authenticate one-time result retrieval, and retain every
provider source field. Repeating that client logic in each product makes callback
state checks, request failure behavior, and numeric identity fidelity easy to
implement inconsistently. Consumers also need an importable package that can be
validated independently from the Go gateway and Console source tree.

## Decision

The standalone Go module `github.com/codetreker/uniauth/sdk/go`, package `uniauth`,
uses Go 1.27.1 and only the standard library. The JavaScript package
`@codetreker/uniauth` emits ESM code and TypeScript declarations for Node.js 24
or later, with no runtime dependencies. Both implement the existing product HTTP
protocol without adding gateway endpoints or changing identity handover.

Clients require a UniAuth base origin and the product's App ID and Secret. They
validate authorization URLs and callback parameters, provide a 32-byte random
state helper, and send one JSON result POST using HTTP Basic authentication.
The gateway base origin requires HTTPS except explicitly enabled loopback HTTP.
The original SDK policy applied that restriction to product callbacks as well.
The [App registration options decision](2026-10-22-app-registration-options.md)
supersedes the callback restriction: callbacks accept absolute HTTP or HTTPS URLs
with a host independently of the base-origin option. Validated callback strings
remain unchanged as authorization query values because callback registration
uses exact matching.

Expected-state checking does not give an SDK ownership of product storage.
Products atomically validate and consume the pending login bound to the browser
before retrieval. SDKs store no pending state, users, account links, or sessions
and expose no administrator API. The
[gateway ownership decision](../architecture/2026-10-12-authentication-gateway.md)
continues to own the five-minute, in-memory, one-time result semantics.

The clients do not follow result redirects or automatically retry retrieval.
Go's default HTTP client has a 30-second timeout; a supplied client controls its
timeout. JavaScript's default deadline is 30,000 milliseconds and composes caller
cancellation. Typed API errors retain status, machine code, message, and request
ID; protocol errors identify malformed responses; transport causes remain
available. Lost, aborted, or unparseable replies may already have consumed the
result, requiring a new login.

The clients expose all seven server-normalized fields: `provider`, `id`, `email`,
`email_verified`, `name`, `avatar_url`, and `origin`. Every field is required in a
response; nullable strings and nullable verification remain distinct from missing
fields, and explicit `false` remains `false`. Missing fields or incompatible types
are protocol errors. The clients do not map provider source fields themselves,
add profile fallbacks, replace identity keys, or expose old wire-field aliases.
The [normalized identity decision](../architecture/2026-10-13-normalized-identity.md)
owns the server mapping and wire format.

Go returns normalized scalar values using `ID`, nullable profile pointers, and
complete `Origin` as `map[string]json.RawMessage`, with the entire response in `RawJSON`.
JavaScript returns the exact snake-case fields and parsed unknown original data
under `origin`, with authoritative `rawJSON`, because ordinary numeric parsing
cannot retain every JSON integer or decimal exactly.

The [SDK reference](../../../../docs/reference/sdks.md) owns client configuration,
usage, and error contracts. Validation uses a separate Go consumer and the packed
JavaScript artifact's bare import and exported declarations. This delivery has
JavaScript package metadata version `0.1.0`; it does not publish a package or tag.

## Alternatives

**Direct HTTP integration.** The existing product protocol remains available to
callers without an SDK, including products using other languages. Such callers
own URL validation, callback parsing, transport classification, and complete
identity handling themselves. The SDKs centralize those repeated client contracts
while leaving browser-bound state and session persistence with each product.
Direct integration is still appropriate where neither package runtime is used.

**Package within the gateway Go module.** A product client package in the root
module would share the gateway's dependency graph and release version, coupling
consumers to service dependencies that the client does not need. A standalone
module preserves a standard-library-only consumer dependency graph and a distinct
nested-module release path. A future release uses a tag such as
`sdk/go/v0.1.0`; no release tag is created by this delivery.

**Project JavaScript numbers into BigInt or strings.** Rewriting large integers
could preserve some values but changes source field types, and it does not solve
exact decimal representation. Keeping the entire raw JSON preserves the original
representations inside `origin` without changing its field types. Native parsed
original data remains a convenience view, and each product chooses an exact
parser if it needs one. The SDK exposes the server's standard profile independently
of that raw-data representation.

The handover alternatives in the gateway decision remain unchanged.

## Consequences

Go consumers can import the standalone module without gateway dependencies, and
Node.js consumers use emitted JavaScript and declarations rather than repository
TypeScript files. Package consumers exercise the artifacts that would be released,
while publication requires a separate authorized release.

Products still need a pending-login store, atomic browser-bound state consumption,
user and permission handling, and their own session lifecycle. A transport failure
cannot make one-time retrieval safely retryable. The SDK's raw response fields
give consumers full identity fidelity; products choose retention and parsing
appropriate to their own data policy.

SDK validation does not establish upstream consent, credentials, live account
authentication, or multi-process state coordination. These client packages add
no central SSO state and do not change the gateway's restart or lost-response
behavior.
