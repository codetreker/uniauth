# API Reference

UniAuth uses a small HTTP protocol for product login and one-time identity
retrieval. Products need a confidential backend with an App Secret. Microsoft
and Google use OIDC upstream; this downstream protocol does not issue ID tokens,
access tokens, refresh tokens, or central login sessions.

Production traffic requires HTTPS. Authentication and Console API responses use
`Cache-Control: no-store`. Requests have an `X-Request-ID` for log correlation.
Errors use this JSON shape unless an unsupported route or HTTP method is handled
by the HTTP router itself:

```json
{"error":{"code":"invalid_request","message":"The authentication request is invalid."}}
```

## Product login

### Start authorization

`GET /auth/authorize` requires exactly one nonempty value for each parameter:

| Parameter | Meaning | Maximum UTF-8 bytes |
| --- | --- | --- |
| `app_id` | Registered, enabled App ID. | 128 |
| `provider` | `microsoft`, `google`, or `github`, configured on the service and allowed by the App. | 32 |
| `redirect_uri` | Exact registered callback URL. | 2048 |
| `state` | Random product login state bound to the initiating browser. | 2048 |

On success UniAuth returns `302` to the provider and creates a ten-minute
transaction. A separate random provider state, PKCE verifier, and OIDC nonce
belong to this transaction. A unique HttpOnly, SameSite=Lax flow cookie binds
it to the browser; simultaneous login tabs have separate cookies. HTTPS flow
cookies use the `__Host-` prefix and Secure attribute.

The product's `state` is preserved independently. It must remain pending in the
product and be validated against that browser before result retrieval. UniAuth
does not establish the product's login session.

```text
GET /auth/authorize?app_id=APP_ID&provider=github&redirect_uri=https%3A%2F%2Fproduct.example%2Fauth%2Fcallback&state=PRODUCT_STATE
```

Invalid or disallowed requests return `400 invalid_request`. Capacity exhaustion
returns `503 capacity_exceeded`. A provider that cannot start authorization
returns `502 provider_failed`. Internal failures return `500 internal_error`.

### Provider callback and product callback

Providers return to `GET /auth/callback/{provider}`. UniAuth validates the
provider state and browser cookie, consumes the transaction, performs the
authorization-code exchange, and validates the identity. An enabled App, allowed
provider, and callback registration are checked again before result creation.

Success returns `302` to the approved product callback:

```text
https://product.example/auth/callback?code=RESULT_CODE&state=PRODUCT_STATE
```

Existing non-reserved callback query parameters remain present. After a valid
transaction has been claimed, provider denial, provider failure, or result
capacity exhaustion returns the same callback with `error` and the original
product `state`, without `code`:

```text
https://product.example/auth/callback?error=provider_denied&state=PRODUCT_STATE
```

Callback error values are `provider_denied`, `provider_failed`, and
`capacity_exceeded`. Malformed provider callbacks return `400 invalid_request`
directly. Missing, expired, already consumed, mismatched, or browser-unbound
transactions return `410 transaction_unavailable` directly. If the App or its
authorization configuration changes during provider completion, the transaction
can fail directly without redirecting to the product.

### Retrieve the identity result

`POST /auth/result` accepts HTTP Basic authentication using App ID as the
username and App Secret as the password. Require `Content-Type: application/json`
and exactly one JSON object containing only `code`. The request body is limited
to 2048 bytes, and the nonempty code to 512 UTF-8 bytes.

```http
POST /auth/result HTTP/1.1
Host: auth.example
Authorization: Basic BASE64_APP_ID_COLON_SECRET
Content-Type: application/json

{"code":"RESULT_CODE"}
```

UniAuth authenticates the currently enabled App and an unrevoked Secret before
accessing the result. It atomically checks App ownership and expiry and deletes
a valid result before writing `200` identity JSON. Wrong credentials and another
App's request do not consume the rightful App's live result. Concurrent valid
claims yield one successful response.

The five-minute lifetime starts when the complete payload is ready and inserted.
A result is unavailable at or after its deadline even if cleanup has not run.
Codes are also lost on restart. A response lost after consumption requires a new
login; repeating the retrieval cannot recover the identity.

| HTTP status | Error code | Meaning |
| --- | --- | --- |
| `400` | `invalid_request` | Invalid JSON, content type, unsupported fields, or code. |
| `401` | `invalid_app` | Missing or invalid credentials, revoked Secret, missing App, or disabled App. |
| `410` | `result_unavailable` | Unknown, expired, consumed, or other-App code. |
| `503` | `service_unavailable` | Authentication service is shutting down or unavailable. |
| `500` | `internal_error` | Application configuration could not be read. |

Unavailable codes reveal no historical reason. An App must use a newly issued
code after a failed or expired login. Products consume their pending `state` on
success or failure and start a fresh flow when needed.

## Identity JSON

The envelope has three fields:

```json
{
  "provider": "github",
  "subject": "github:123456",
  "sources": {
    "profile": {"id": 123456, "login": "example", "email": null},
    "emails": [{"email": "person@example.com", "primary": true, "verified": false}]
  }
}
```

Source JSON retains all obtained fields, including unknown attributes, explicit
nulls, false booleans, arrays, and nested objects. Missing source fields remain
missing. Products must tolerate provider fields being absent and must choose
their own account-linking and email-verification policy. Whitespace and JSON
object ordering are not contractual.

| Provider | Stable `subject` | `sources` |
| --- | --- | --- |
| Microsoft | `microsoft:9188040d-6c67-4c5b-b112-36a304b66dad:<oid>` | Complete validated `id_token_claims`, Graph `/me` as `profile`, and `photo`. |
| Google | `google:<sub>` | Complete validated `id_token_claims` and fetched `userinfo`, whose `sub` must match the ID token. |
| GitHub | `github:<numeric id>` | Authenticated `/user` as `profile`, and all pages of `/user/emails` as `emails`. |

Microsoft `photo` is `null` when Graph returns `404`. Otherwise it is an object
with `content_type` and standard Base64 `data_base64`, preserving returned bytes.
Other photo errors, invalid image content, profile failures, and email-fetch
failures fail authentication. Google and GitHub image URLs remain in their
source objects; those images are not downloaded.

Subjects are independent of product App Secrets. Email, display name, and login
name are attributes, not identity keys. Microsoft requires the personal tenant
and a validated `oid`; there is no fallback identifier. OAuth credentials,
raw signed ID tokens, upstream authorization codes, and access or refresh tokens
are excluded from the identity package.

## Console API

The embedded UI lives at `/console/`; `/` redirects there. API clients use
`/console/api/` on the same origin. Every mutation requires exactly one `Origin`
header equal to the browser-serialized origin derived from `UNIAUTH_PUBLIC_URL`.
Configured domain names are converted to ASCII with IDNA and lowercased; valid
IPv6 literals are serialized canonically; numeric ports lose leading zeros and
default HTTPS `443` or HTTP `80` ports are omitted. For example,
`https://AUTH.example:443/` expects `Origin: https://auth.example`.

Only the configured origin is normalized. Incoming headers remain subject to
exact string comparison: `null`, trailing slashes, different ports or hosts,
multiple values, and noncanonical spellings are rejected. Authenticated mutations
also require `X-CSRF-Token` from login or the session endpoint. JSON bodies require
`application/json`, contain only supported fields, and are limited to 64 KiB.

### Administrator session

| Method and path | Body | Success |
| --- | --- | --- |
| `POST /console/api/login` | `{"password":"..."}` | `200 {"csrf_token":"..."}` and session cookie. |
| `GET /console/api/session` | None | `200 {"csrf_token":"..."}`. |
| `POST /console/api/logout` | None | `204`, session removed and cookie cleared. |

The administrator session has a fixed eight-hour lifetime in process memory.
The HttpOnly, SameSite=Strict cookie is Secure and named
`__Host-uniauth_admin` under HTTPS. Explicit local HTTP uses `uniauth_admin`.
Login replaces a prior session presented by the same browser; logout, expiry,
or restart invalidates it. This session grants Console authority only.

Password failures return `401 unauthorized`. Login rate and concurrency limits
return `429 rate_limited` with `Retry-After`. Session capacity exhaustion returns
`503 session_capacity`. Missing sessions return `401 unauthorized`; invalid
Origin or CSRF returns `403 invalid_origin` or `403 invalid_csrf`.

### Applications and Secrets

App creation and replacement use this input:

```json
{
  "name": "Example product",
  "enabled": true,
  "redirect_uris": ["https://product.example/auth/callback"],
  "providers": ["microsoft", "google", "github"]
}
```

Names contain 1–128 UTF-8 bytes without surrounding whitespace. Each App has
1–16 unique callbacks and 1–3 unique supported provider IDs. Callback URLs
require HTTPS, a host, and no user information or fragment. Explicit loopback
HTTP is allowed only when enabled by deployment configuration. Callback query
parameters `code`, `state`, and `error` are reserved and cannot be registered.
Matching is against the registered string exactly; there are no wildcard rules.

| Method and path | Success |
| --- | --- |
| `GET /console/api/apps` | `200 {"apps":[...]}`. |
| `POST /console/api/apps` | `201 {"app":{...},"secret":"...","secret_id":"..."}`. |
| `GET /console/api/apps/{id}` | `200` App object. |
| `PUT /console/api/apps/{id}` | `200` replaced App object; App ID and creation time remain unchanged. |
| `DELETE /console/api/apps/{id}` | `204`, App and its Secrets deleted. |
| `GET /console/api/apps/{id}/secrets` | `200 {"secrets":[...]}` with metadata only. |
| `POST /console/api/apps/{id}/secrets` | `201 {"id":"...","secret":"...","created_at":"..."}`. |
| `DELETE /console/api/apps/{id}/secrets/{secretID}` | `204`, credential revoked; repeating an existing revocation is idempotent. |
| `GET /console/api/providers` | `200 {"providers":[{"id":"microsoft","available":true},...]}` for all three providers. |

App objects contain `id`, input fields, `created_at`, and `updated_at`. Secret
metadata contains `id`, `created_at`, and `revoked_at` when revoked. Timestamp
values use RFC 3339 JSON strings. A new plaintext Secret is returned only by
creation; subsequent reads return metadata. Rotation creates a new credential,
allows the product to switch, then revokes the previous one. Multiple unrevoked
credentials may coexist. Disabling an App blocks authorization and retrieval;
deletion also removes its credential records.

Invalid input returns `400 invalid_input`, unsupported content type returns
`415 invalid_content_type`, missing records return `404 not_found`, unsupported
methods return `405 method_not_allowed`, and storage failures return
`500 internal_error`. Provider availability describes deployment configuration;
an unavailable provider cannot start authentication even if listed on an App.

The Console UI serializes configuration editing, enable/disable changes, and
App deletion for the same App. Opening an editor or delete confirmation reserves
that App; conflicting controls remain unavailable through completion or
cancellation. A pending update shows `Updating…`, then adopts the returned App
object before another action can start. Opening the next editor uses that latest
snapshot, so saving a name after a completed disable retains the disabled state.
Repeated submit attempts do not start a second mutation.

After a non-authentication configuration failure, the UI retains ownership while
reloading server configuration and merges only the affected App into its current
snapshot. If the reload also fails, `State unavailable` blocks new configuration
actions until an explicit Refresh succeeds. An already-open editor or delete
confirmation still owns the App and permits Retry or Cancel. A successful owned
retry may leave the unavailable marker until Refresh. Administrator session
expiry returns to login.
These controls protect one Console instance; API callers and separate browsers
still send full App replacements without server-side version checks.

Management mutations that pass Origin, administrator session, and CSRF checks
receive `X-Operation-ID`. Their completion emits a `console management operation`
log with `operation_id`, `request_id`, `action`, `affected_app_id`,
`phase=completed`, `outcome`, `error_category`, and HTTP `status`. Actions are
`app.create`, `app.update`, `app.delete`, `secret.create`, and `secret.revoke`.
Creation events use the generated App ID; existing-App events use a syntactically
validated App ID. Invalid input or storage failures are recorded as failed
operations. Logs exclude credentials and App display data. Authorization failures
remain observable in HTTP request logs and do not invoke the management handler.

## Health

`GET /healthz` returns `200 {"status":"ok"}`. `GET /readyz` also verifies an
application-store read and returns `503` if that fails. These endpoints do not
perform an interactive upstream login or prove provider credentials valid.
