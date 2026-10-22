# Product SDK Reference

The Go and JavaScript SDKs connect confidential product backends to UniAuth's
[existing HTTP protocol](api.md). They build authorization URLs, generate random
state, validate callback parameters, and retrieve a one-time identity result.
They do not store pending logins, users, account links, sessions, or credentials
outside the configured client, and provide no Console management API.

| Package | Runtime and artifact |
| --- | --- |
| `github.com/codetreker/uniauth/sdk/go` | Standalone Go 1.27.1 module, package `uniauth`, standard library only. |
| `@codetreker/uniauth` | Node.js 24 or later, ESM JavaScript and TypeScript declarations, no runtime dependencies. |

JavaScript package metadata uses version `0.1.0`. This delivery validates local
source and packed artifacts; it does not establish an npm publication or Go
release tag. A future Go SDK release uses the nested-module tag format, such as
`sdk/go/v0.1.0`. Package-local examples live in the [Go README](../../sdk/go/README.md)
and [JavaScript README](../../sdk/js/README.md).

## Login ownership and sequence

1. Generate an unpredictable state and store it in a pending login bound to the
   initiating product browser. Both state helpers use 32 cryptographically random
   bytes encoded as unpadded Base64url.
2. Build an authorization URL with the selected provider, exact registered
   callback, and that state; redirect the browser there.
3. Parse the product callback against the expected state. Require a single
   matching state and exactly one nonempty `code` or `error`.
4. Atomically validate and consume the pending login bound to that browser before
   starting result retrieval. This is a product-store operation, not an SDK
   operation. Concurrent callbacks must not both reach the retrieval call.
5. Call retrieval once with the result code, then apply product user, linking,
   permission, and session policy to the returned identity.

Callback parsing checks parameters against supplied expected state; it cannot
prove browser binding, consume product state, or prevent a product from parsing
the same callback twice. Put expected-state validation and pending-state
consumption in the same product atomic operation. Matching only the callback's
state value without product-owned browser binding is insufficient.

A recognized provider-error callback with validated expected state terminates the
pending login. Invalid or mismatched callbacks are rejected before retrieval. A
lost, aborted, or malformed retrieval response may follow server-side consumption.
Start a fresh login; SDKs do not retry the same code or recover a consumed result.

## Client configuration

Clients require the UniAuth base origin, Console-generated App ID, and product
App Secret. These differ from UniAuth's deployment provider credentials. Secrets
and retrieval remain on the product backend; authorization URLs contain no Secret.

| Meaning | Go `Config` | JavaScript client options |
| --- | --- | --- |
| UniAuth base origin | `BaseURL` | `baseURL` |
| Product App ID | `AppID` | `appID` |
| Product App Secret | `AppSecret` | `appSecret` |
| Explicit development HTTP for the gateway base origin | `AllowLoopbackHTTP` | `allowLoopbackHTTP` |
| Custom request transport | `HTTPClient` | `fetch` |
| Request deadline | Default HTTP client: 30 seconds; supplied client controls its timeout. | `timeoutMs`, default 30,000 milliseconds. |

Base URLs are absolute origins without user information, query, fragment, or
application path. HTTPS is required unless loopback HTTP is explicitly enabled.
Authorization callbacks accept absolute HTTP or HTTPS URLs with a host,
independently of the base-origin option. They reject credentials, fragments, and
reserved `code`, `state`, or `error` query parameters.
The validated callback's original string is retained as the `redirect_uri` query
value, because the service matches registration strings exactly.

App IDs contain 1–128 UTF-8 bytes and no colon; Secrets contain 1–512 UTF-8 bytes.
States and callbacks contain 1–2048 UTF-8 bytes, and result codes 1–512. Providers
are limited to `microsoft`, `google`, and `github`. Invalid data fails locally;
registered callback membership and provider availability remain service checks.
JavaScript `timeoutMs` must be an integer from 1 to 2,147,483,647.

Request construction remains fixed with a configured transport: one
`POST /auth/result`, HTTP Basic App ID/Secret authentication, and JSON `code`.
Redirects are not followed, and the SDK adds no automatic retry. Callers supplying
a transport must preserve those properties and the configured destination.

## Go

Create a client once and keep it in the product backend:

```go
client, err := uniauth.New(uniauth.Config{
    BaseURL:   "https://auth.example",
    AppID:     appID,
    AppSecret: appSecret,
})
if err != nil {
    return err
}
```

Import the package as `github.com/codetreker/uniauth/sdk/go`. `AuthorizationURL`
accepts an `AuthorizationRequest` with provider, redirect URI, and state.
`uniauth.GenerateState()` returns a string. Use provider constants `uniauth.Microsoft`,
`uniauth.Google`, or `uniauth.GitHub`:

```go
state := uniauth.GenerateState()
loginURL, err := client.AuthorizationURL(uniauth.AuthorizationRequest{
    Provider:    uniauth.GitHub,
    RedirectURI: "https://product.example/auth/callback",
    State:       state,
})
if err != nil {
    return err
}
```

Store that state bound to the product browser before redirecting to `loginURL`.
`uniauth.ParseCallback(callbackQuery, expectedState)` accepts `url.Values` and
returns `(string, error)`. When starting from a raw query, use `url.ParseQuery`
and reject its parse error before passing the decoded values. Duplicate state,
duplicate code/error, mismatch, or ambiguous success/error values fail. Run that check within the product's atomic
pending-login validation and consumption operation, then call
`client.Retrieve(ctx, code)` after the pending login has been consumed. It returns
`(*Identity, error)` and respects context cancellation.

A supplied `HTTPClient` is copied so redirects can be disabled without modifying
the caller's client. Its timeout, transport, and cookie jar remain in use. Clients
are safe for concurrent use when those supplied components are also safe.

`Identity` exposes the server's seven-field normalized identity:

| Go field | Wire field | Go type |
| --- | --- | --- |
| `Provider` | `provider` | `Provider` |
| `ID` | `id` | `string` |
| `Email` | `email` | `*string` |
| `EmailVerified` | `email_verified` | `*bool` |
| `Name` | `name` | `*string` |
| `AvatarURL` | `avatar_url` | `*string` |
| `Origin` | `origin` | `map[string]json.RawMessage` |

All seven fields must be present in the response. The provider must be supported,
ID must be nonempty, `origin` must be an object, and profile values must have the
expected nullable types; missing or incompatible fields produce `ProtocolError`.
A JSON `null` profile value becomes a nil pointer, while `email_verified: false`
becomes a non-nil pointer to `false`. The SDK does not derive profile values from
`origin`, replace IDs, or add aliases for the previous wire shape.

`Origin` retains the complete original source object and `RawJSON` the entire
response bytes, including unknown envelope fields. Keep those raw values when
exact integer or decimal representation matters. Original fields, nulls, and
false values are preserved without projection.

## JavaScript and TypeScript

After installing the local package artifact, import its ESM entry point:

```ts
import { createClient, generateState, parseCallback } from "@codetreker/uniauth";

const client = createClient({
  baseURL: "https://auth.example",
  appID,
  appSecret,
});
const state = generateState();
```

The client provides `authorizationURL({ provider, redirectURI, state })`:

```ts
const loginURL = client.authorizationURL({
  provider: "github",
  redirectURI: "https://product.example/auth/callback",
  state,
});
```

Store state bound to the product browser before redirecting to `loginURL`.
`parseCallback(callbackURL.searchParams, expectedState)` accepts `URLSearchParams`
and returns a code or throws `CallbackError`. Perform that validation within the
product's atomic pending-login consumption before calling
`await client.retrieve(code, { signal })`. The optional caller AbortSignal is
combined with the configured deadline. The package is intended for Node.js product
backends, not browser Secret storage.

Returned identities contain `provider`, `id`, `email`, `email_verified`, `name`,
`avatar_url`, `origin`, and the additional `rawJSON` field. The seven wire fields
are mandatory. `provider` is a supported provider, `id` is a nonempty string,
`email`, `name`, and `avatar_url` are strings or `null`, `email_verified` is a
boolean or `null`, and `origin` is an object. Missing fields or incompatible types
throw `ProtocolError`. Null values and explicit `false` remain unchanged.

These are the server's normalized values; the SDK does not derive profile data
from `origin`, invent missing fields, or return previous-shape aliases. Original
values inside `origin` remain unknown JSON values. JavaScript parsing retains
nested data, nulls, and false booleans, but JSON numbers become JavaScript numbers. Integers beyond the safe range or exact decimal
values can lose precision in that parsed view. The complete `rawJSON` string is
authoritative for numeric fidelity; preserve it or use an appropriate exact JSON
parser when the product needs that representation.

## Errors and one-time transport

Rejected client or authorization input produces an ordinary Go error or JavaScript
`TypeError` before retrieval. Callback validation uses `CallbackError` instead.

Go exposes `*APIError` with `StatusCode`, `Code`, `Message`, and `RequestID`.
Use `errors.As` to distinguish it from `*ProtocolError`, which has `StatusCode`,
`RequestID`, `Reason`, and `Cause`. `*RequestError` wraps request/context failures
and retains `Cause`; both wrapped error types support `errors.Is` and `errors.As`
through `Unwrap`. A response-body failure is a `ProtocolError` with reason
`response_body` and its original cause. Ordinary error formatting omits identity
payloads and request credentials.

JavaScript exports `APIError` with `status`, `code`, `message`, and `requestID`,
and `ProtocolError` with `status`, `message`, and `requestID`. An absent request
ID is `null` in JavaScript and an empty string in Go. Fetch and body-read failures
remain the original thrown transport error. Both packages expose `CallbackError`, with
Go field `Code` and JavaScript field `code` equal to `invalid_callback`,
`state_mismatch`, `provider_denied`, `provider_failed`, or `capacity_exceeded`.

| Failure | SDK result |
| --- | --- |
| Invalid client configuration or authorization input | Explicit validation error before sending a result request. |
| Invalid callback parameters or state | Typed callback validation error. |
| Valid provider callback carrying `error` | Typed provider callback error preserving the category. |
| Structured non-success UniAuth response | `APIError` carrying HTTP status, error code, message, and request ID. |
| Malformed success/error JSON or identity envelope | `ProtocolError`. |
| Network, deadline, cancellation, or response-body failure | Original transport cause remains available. |

The request ID comes from `X-Request-ID` when supplied by UniAuth and supports
server log correlation. Error classification does not reveal whether a network
failure happened before or after server consumption. Parsing a successful reply
can also fail after the result was removed. The product must initiate a new
login rather than add a retry around retrieval.

## Local package use and validation

For a Go consumer using a local checkout, add a module replacement for the
standalone SDK module pointing to that checkout's `sdk/go/` directory. The client
does not import gateway `internal/` packages or require the gateway executable.

For JavaScript, build `sdk/js/`, pack it as a tarball, and install the tarball in
the consumer. The package export resolves emitted `dist/index.js` and
`dist/index.d.ts`. Use a bare `@codetreker/uniauth` import, rather than importing
repository TypeScript source. That checks the code and declaration files actually
present in the distributable artifact.

Repository gates validate standalone Go coverage and external Go imports, and
JavaScript runtime behavior, exported declarations, and packed bare imports.
The temporary consumer installs only the SDK tarball; its declaration check uses
the pinned compiler and Node ambient types from frozen SDK setup. SDK imports
resolve to the installed artifact without source aliases or registry metadata.
Integration tests run both clients against the actual Go authentication handler,
SQLite App registry, and browser-bound provider callback using a fixture provider.
They check wrong credentials and App ownership without consumption, one-time
replay rejection, concurrent claims, expiry, and complete raw identity sources.
See the [original SDK task receipt](../tasks/2026-10-13-product-sdks.md) and
[current SDK/main integration receipt](../tasks/2026-10-13-sdk-main-integration.md)
for their respective observed results.
Local checks do not prove a registry release or a live third-party login.
