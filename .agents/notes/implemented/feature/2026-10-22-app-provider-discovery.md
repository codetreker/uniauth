# Agent Note: App-Aware Provider Discovery

Status: implemented

## Problem

A product login page needs choices that its registered App can use at the exact
product callback. The Console's public provider flags describe service-wide
configuration, so they cannot establish App permission or callback registration.
Maintaining another provider list in each product can display choices after an
App or deployment configuration has changed. Product backends need an
authoritative answer without obtaining a Console owner's management session.

## Decision

`POST /auth/providers` accepts HTTP Basic App ID/Secret authentication and JSON
containing only `redirect_uri`. It reads the current App and credential records,
requires an enabled App and a live credential, validates the callback syntax and
exact registered string, and returns the intersection of the App's allowed
providers and the configured server registry. The result uses fixed
`microsoft`, `google`, `github` order and includes a non-null empty array when no
provider is in that intersection. It uses the same App credentials as identity
retrieval, with no new credential type, storage, or deployment configuration.

The request body is bounded to 16 KiB and callbacks to 1–2048 UTF-8 bytes.
Missing or disabled Apps and missing, invalid, reset, or revoked credentials
return `401 invalid_app` under the existing result-authentication rules. Invalid input
or an unregistered callback returns `400 invalid_request`; configuration-read
failures return `500 internal_error`; a closed authentication service returns
`503 service_unavailable`. The endpoint uses `Cache-Control: no-store` and
`Referrer-Policy: no-referrer`. Request bodies and credentials stay out of logs.

Discovery creates no transaction, identity result, browser cookie, or session.
It reports configuration availability without probing upstream credentials,
health, consent, or account authentication. The answer is a snapshot, not a
reservation: authorization still checks current App, provider, and callback
eligibility when the browser navigates to UniAuth.

The Go client adds `Providers(ctx, redirectURI) ([]Provider, error)`. It validates
callback syntax locally, retains its exact registered string, and sends one
context-bound authenticated POST without redirects or retries. The response is
bounded to 16 KiB and requires a non-null array of unique, known provider values.
Unknown response fields are tolerated, while duplicate JSON keys are rejected.
An empty array is valid; invalid data is a `ProtocolError`. Existing `APIError`
and `RequestError` classifications retain gateway and transport failures without
substituting an empty result. Caller cancellation and the configured HTTP client
deadline bound each request.

The [original SDK decision](2026-10-13-product-sdks.md) owns login-and-retrieval
client responsibilities and its historical choice to implement existing gateway
endpoints. Discovery is an additive extension with its own HTTP contract. The
JavaScript SDK keeps its existing API; confidential JavaScript and other-language
backends can use the HTTP endpoint directly. The
[blueprint](../../../../docs/blueprint/README.md#app-aware-provider-discovery)
owns the architecture, the [API reference](../../../../docs/reference/api.md#discover-configured-providers)
owns the wire contract, and the [SDK reference](../../../../docs/reference/sdks.md#go)
owns Go usage and errors.

## Alternatives

**Global Console provider flags.** The unauthenticated Console endpoint is useful
for Console sign-in, but omits an external App's provider allowlist, enabled state,
credentials, and callback registration. A product cannot use those flags as its
own authoritative login choices.

**Duplicate product configuration.** A product-maintained provider allowlist avoids
a discovery request, but duplicates App and deployment settings. It can advertise
unavailable choices after either owner changes configuration and requires each
product to coordinate those changes manually.

**Console App-management API.** The existing App metadata includes callbacks and
allowed providers, but requires a platform owner's Console session and exposes
management details a product backend does not need. An App Secret cannot use that
owner-only management API. A narrow authenticated endpoint keeps platform-owner
authority separate from product credentials.

## Consequences

Products can render configured login choices from the authoritative App and
registry records. Subsequent requests observe App removal, disabling, credential
reset or revocation, provider allowlist changes, and callback changes. Each
request incurs credential authentication and a configuration lookup; no cache
or new persistent state can hide those changes. A valid empty list remains
distinct from a network, protocol, or configuration failure.

App Secrets remain backend-only. Products may project the returned choices into
their own unauthenticated login UI, but must not expose App credentials to browser
code. Request and response bounds limit discovery transport costs, and its
context follows caller cancellation. Discovery success does not establish live
provider credentials or successful login.

Adding a JavaScript SDK convenience method later is additive: the stable HTTP
endpoint is already available and existing login, callback, and identity types
need no redesign. JavaScript callers currently own direct HTTP request and
response handling for discovery. This delivery does not publish SDK packages,
create release tags, deploy UniAuth, or establish production provider health.
