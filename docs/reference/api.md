# API Reference

UniAuth uses a small HTTP protocol for product login and one-time identity
retrieval. Products need a confidential backend with an App Secret. Microsoft
and Google use OIDC upstream; this downstream protocol does not issue ID tokens,
access tokens, refresh tokens, or central login sessions.

[Go and JavaScript SDKs](sdks.md) implement these product endpoints without changing
the protocol. They validate URLs and callback parameters and preserve complete
identity data. Products retain atomic browser-bound pending-state consumption and
session ownership. A retrieval SDK sends one POST and does not follow redirects
or retry a consumed code.

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

Every successful response includes these seven fields:

```json
{
  "provider": "github",
  "id": "github:123456",
  "email": "person@example.com",
  "email_verified": false,
  "name": null,
  "avatar_url": "https://avatars.githubusercontent.com/u/123456",
  "origin": {
    "profile": {
      "id": 123456,
      "login": "example",
      "email": null,
      "name": null,
      "avatar_url": "https://avatars.githubusercontent.com/u/123456"
    },
    "emails": [{"email": "person@example.com", "primary": true, "verified": false}]
  }
}
```

| Field | JSON type | Meaning |
| --- | --- | --- |
| `provider` | String | `microsoft`, `google`, or `github`. |
| `id` | Nonempty string | Stable provider-qualified identity key. |
| `email` | String or `null` | Selected provider email, without rewriting its value. |
| `email_verified` | Boolean or `null` | Upstream verification value when available; unknown is `null`, and explicit `false` remains `false`. |
| `name` | String or `null` | Provider display name; usernames are not substituted for absent names. |
| `avatar_url` | String or `null` | Provider image URL, or a data URL containing the fetched Microsoft photo. |
| `origin` | Object | Complete source-specific identity data obtained under granted scopes. |

Missing, null, and empty standard string attributes become `null`. Known fields
used by the mapping must have the expected JSON type when present and non-null;
a type mismatch fails authentication, including in a lower-priority source
field. Unknown source fields retain their original meaning. The API has no
`subject` or `sources` aliases. Whitespace and JSON object ordering are not
contractual.

SDKs validate that all seven fields are present with these types and retain
nullable values and explicit `false`. They expose the server's profile values
without deriving replacements from `origin`. Go retains `origin` as `Origin`
(`map[string]json.RawMessage`) and the entire received body as `RawJSON`; JavaScript retains
parsed unknown original data as `origin` and the body as `rawJSON`. Parsed
JavaScript numbers inside `origin` can lose integer or decimal precision; use
the retained raw JSON when exact representation matters.

### Provider field mapping

| Provider | `email` | `email_verified` | `name` | `avatar_url` |
| --- | --- | --- | --- | --- |
| Microsoft | Nonempty Graph `profile.mail`, otherwise nonempty ID-token claim `email`. | `null`; the selected sources provide no verification flag. | Nonempty Graph `profile.displayName`, otherwise nonempty ID-token claim `name`. | `data:<media-type>;base64,<base64-data>` from the fetched photo; `null` on Graph photo `404`. |
| Google | UserInfo `email`. | UserInfo `email_verified`, preserving boolean or `null` independently of whether an email is present. | UserInfo `name`. | UserInfo `picture`. |
| GitHub | Nonempty primary email from the complete `emails` list, otherwise nonempty public `profile.email`; `null` when neither is available. | `verified` from an email-list entry whose `email` exactly matches the selected value; `null` without a selected email, matching entry, or verification value. | Profile `name`. | Profile `avatar_url`. |

Google UserInfo is the canonical profile source; ID-token claims remain in
`origin` without supplying substitute profile values. Microsoft
`preferred_username` and `userPrincipalName` are not email fallbacks. GitHub
never selects an arbitrary non-primary email. Authentication success and an
upstream verification flag do not establish a product's account-linking,
email-ownership, or permission policy; each product owns those decisions.

### Stable identity and original sources

| Provider | Stable `id` | `origin` |
| --- | --- | --- |
| Microsoft | `microsoft:9188040d-6c67-4c5b-b112-36a304b66dad:<oid>` | Complete validated `id_token_claims`, Graph `/me` as `profile`, and `photo`. |
| Google | `google:<sub>` | Complete validated `id_token_claims` and fetched `userinfo`, whose `sub` must match the ID token. |
| GitHub | `github:<numeric id>` | Authenticated `/user` as `profile`, and all pages of `/user/emails` as `emails`. |

Source JSON retains all obtained fields, including unknown attributes, explicit
nulls, false booleans, arrays, and nested objects. Missing source fields remain
missing; normalization never inserts or rewrites fields inside `origin`.

Microsoft `origin.photo` is `null` when Graph returns `404`. Otherwise it is an
object with `content_type` and standard Base64 `data_base64`, preserving returned
bytes. The corresponding `avatar_url` uses the parsed image media type and the
same Base64 data. It requires no additional fetch; both representations count
toward the result's memory limit. Other photo errors, invalid image content,
profile failures, and email-fetch failures fail authentication. Google and
GitHub image URLs are not downloaded.

IDs are independent of product App Secrets. Email, display name, and login name
are attributes, not identity keys. Microsoft requires the personal tenant and a
validated `oid`; there is no fallback identifier. OAuth credentials, raw signed
ID tokens, upstream authorization codes, and access or refresh tokens are
excluded from the identity package.

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
also require `X-CSRF-Token` from the session endpoint. JSON bodies require
`application/json`, contain only supported fields, and are limited to 64 KiB.

### OAuth login and Console session

The Console is itself a protected App, managed by service startup. Anyone can
sign in through any configured provider. Provider availability is public:

| Method and path | Body | Success |
| --- | --- | --- |
| `GET /console/api/providers` | None | `200 {"providers":[{"id":"microsoft","available":true},...]}` for all three providers. |
| `POST /console/api/login` | `{"provider":"google"}` | `200 {"authorization_url":"..."}` and browser-binding cookie. |
| `GET /console/auth/callback` | Gateway callback `code` or `error`, plus `state`. | `303` to `/console/` after login or linking. |
| `GET /console/api/session` | None | `200 {"csrf_token":"...","user":{...}}`. |
| `POST /console/api/logout` | None | `204`, session removed and cookie cleared. |

Login has no password field. The selected provider must be configured; otherwise
it returns `400 invalid_provider`. Login requires the exact Origin but no prior
session or CSRF token. Follow the returned authorization URL in the browser.
The Console App uses the ordinary gateway flow and result retention rules.
Its callback retrieves the result through the same authenticated handover operation
in process; its App Secret is never sent to the browser.

While starting sign-in or identity linking, the selected Console button shows
`Connecting…`. Other provider buttons and conflicting workspace controls remain
disabled while that attempt owns the Console. OAuth progress does not insert the
unfinished App/Secret warning or resize the sign-in card. Start failures display
the error and permit retry; an interactive preview releases the controls on
completion. Where the Navigation API is available, stopping navigation before
departure restores the controls through that attempt's abort signal. A fixed
`Cancel sign-in` control is available once navigation starts, including when
that API is unavailable or navigation reports no completion. It stops the
browser load before releasing the controls. Returning to a cached page through
browser history also restores them. Linking keeps the current identity list
visible until departure; cancellation does not reload or fabricate an identity.

Console pending login state has a ten-minute lifetime and a separate random
HttpOnly, SameSite=Lax browser-binding cookie. State and binding are consumed
atomically. Missing or wrong binding, expired state, replay, and malformed or
ambiguous callback fields fail without creating a session. The callback accepts
exactly one nonempty `state` and either `code` or `error`, with no extra fields.
After processing it redirects to a clean Console URL; on failure that URL contains
only a safe `login_error` category: `invalid_callback`, `login_unavailable`,
`provider_failed`, `login_failed`, `identity_conflict`, or `session_capacity`.
Identity profiles and result codes are never forwarded to the Console page.

The Console session has a fixed eight-hour lifetime in process memory. Its
HttpOnly, SameSite=Strict cookie is Secure and named `__Host-uniauth_console`
under HTTPS; explicit local HTTP uses `uniauth_console`. A successful login
rotates its session and CSRF token. Logout, expiry, or restart invalidates it.
These sessions authorize Console access only and establish no product session
or central SSO session. Missing sessions return `401 unauthorized`; invalid
Origin or CSRF returns `403 invalid_origin` or `403 invalid_csrf`. Pending login
capacity exhaustion returns `503 login_capacity`; session capacity exhaustion
is reported through the callback redirect.

The session's `user` contains `id`, nullable `email`, `name`, and `avatar_url`,
`is_admin`, and `created_at`. Profile strings are nullable and timestamps use
RFC 3339. A successful sign-in refreshes account email, name, and avatar from that
identity, preserving nulls. Linking refreshes the linked identity's metadata but
does not change the account display profile. The account ID is a generated platform ID; it is distinct from its
linked provider IDs.

### Platform accounts and linking

A previously unseen stable provider-qualified identity ID creates a Console
account. A known linked ID signs in to its existing account. Matching emails never
merge accounts or move identities.

| Method and path | Body | Success |
| --- | --- | --- |
| `GET /console/api/identities` | None | `200 {"identities":[...]}` for the authenticated account only. |
| `POST /console/api/identities/link` | `{"provider":"github"}` | `200 {"authorization_url":"..."}` and a separate flow cookie. |

Each linked identity contains `provider`, stable `id`, nullable `email`,
`email_verified`, and `name`, plus `linked_at`. Verification is boolean or `null`;
unknown is not treated as verified. No raw provider `origin` or token is stored
or returned by these account endpoints.

Linking requires a live authenticated session, exact Origin, and CSRF. Its
pending flow records that initiating account and session. The callback must
retain the correct browser binding and the initiating session must remain live
through final attachment; logout or expiry prevents the link. A Strict session
cookie may be absent after a cross-site provider redirect, so the separate Lax
flow cookie supplies browser binding. If a session cookie is present, it must
identify the initiating session. An identity already attached to the same account
can be linked idempotently; one belonging to another account fails with the
callback's `identity_conflict` category. Signing in to that other identity first
creates a separate account, so link additional identities from the existing
account's controls.

### Administrator visibility

Initial administrator authority is claimed once by the first Google or GitHub
normalized identity whose email matches `UNIAUTH_INITIAL_ADMIN_EMAIL`
case-insensitively and has
`email_verified=true`. Microsoft email verification is `null` and cannot claim
bootstrap authority. This trusts the provider's verification assertion; it does
not perform a fresh mailbox challenge. The claim attaches authority to the actual
platform account. Subsequent identity-email or deployment-email changes do not
transfer the role. See [configuration](configuration.md) for bootstrap operations.

`GET /console/api/users?cursor=OPAQUE_CURSOR&limit=50` requires administrator
authority, checked against durable account state. Its response is:

```json
{
  "users": [{
    "id": "PLATFORM_USER_ID",
    "email": "person@example.com",
    "name": "Example person",
    "created_at": "2026-10-13T00:00:00Z",
    "app_count": 2
  }],
  "next_cursor": null
}
```

The default page size is 50; `limit` must be a decimal integer from 1 through 100.
`cursor` is an opaque continuation value. Queries permit each supported parameter
once; extra or duplicate parameters, malformed cursors, and invalid limits return
`400 invalid_input`. `next_cursor=null` means no following page. Ordinary users
receive `403 forbidden`.

User summaries contain only `id`, nullable `email` and `name`, `created_at`, and
`app_count`. The system Console App is excluded from counts. Administrators have
no API to inspect other users' identities, App details, callbacks, providers,
Secrets, or management actions. Administrator authority grants no owner bypass.

### Applications and Secrets

All App and Secret operations are scoped to the authenticated platform account.
The service derives ownership from the session; requests supply no owner ID.
Listing returns only that account's Apps. Reading, changing, or addressing
another account's App or the protected system Console App returns `404 not_found`,
including for an administrator. Ownership and system-App checks occur within the
same query or transaction as each operation.

App creation requires application configuration and metadata for its initial
Secret:

```json
{
  "name": "Example product",
  "enabled": true,
  "redirect_uris": ["https://product.example/auth/callback"],
  "providers": ["microsoft", "google", "github"],
  "initial_secret": {
    "name": "Production backend",
    "description": "Used by the production API.\nRotate after deployment."
  }
}
```

`PUT /console/api/apps/{id}` accepts only the application configuration fields
`name`, `enabled`, `redirect_uris`, and `providers`; it does not create or edit a
Secret. App names contain 1–128 UTF-8 bytes without surrounding whitespace.
Each App has 1–16 unique callbacks and 1–3 unique supported provider IDs. Callback
URLs require HTTPS, a host, and no user information or fragment. Explicit
loopback HTTP is allowed only when enabled by deployment configuration. Callback
query parameters `code`, `state`, and `error` are reserved and cannot be
registered. Matching is against the registered string exactly; there are no
wildcard rules.

Secret creation and detail updates accept this JSON object:

```json
{
  "name": "Production backend",
  "description": "Used by the production API.\nRotate after deployment."
}
```

The initial Secret object and every Secret name are required. A Secret name
contains 1–128 valid UTF-8 bytes, with no surrounding whitespace, CR, or LF.
`description` is optional: omission stores the empty string; a supplied value
must be a JSON string containing at most 4096 valid UTF-8 bytes. Empty
strings, surrounding whitespace, and multiline descriptions are preserved
exactly. Supplied `null` is invalid for either field. Names may repeat; the
Secret ID identifies a particular credential.

| Method and path | Success |
| --- | --- |
| `GET /console/api/apps` | `200 {"apps":[...]}`. |
| `POST /console/api/apps` | `201 {"app":{...},"secret":"...","secret_id":"..."}`, including the one-time plaintext and its ID. |
| `GET /console/api/apps/{id}` | `200` App object. |
| `PUT /console/api/apps/{id}` | `200` replaced App object; App ID and creation time remain unchanged. |
| `DELETE /console/api/apps/{id}` | `204`, App and its Secrets deleted. |
| `GET /console/api/apps/{id}/secrets` | `200 {"secrets":[...]}` with metadata for active and revoked credentials. |
| `POST /console/api/apps/{id}/secrets` | `201 {"id":"...","name":"Production backend","description":"...","secret":"...","created_at":"..."}`. |
| `PUT /console/api/apps/{id}/secrets/{secretID}` | `200` updated Secret metadata, whether active or revoked. |
| `POST /console/api/apps/{id}/secrets/{secretID}/reset` | `201 {"id":"...","name":"Production backend","description":"...","secret":"...","created_at":"..."}` for a replacement credential. |
| `DELETE /console/api/apps/{id}/secrets/{secretID}` | `204`, credential revoked; repeating an existing revocation is idempotent. |

App objects contain `id`, configuration fields, `created_at`, and `updated_at`.
Secret metadata always contains `id`, `name`, `description`, and `created_at`,
plus `revoked_at` when revoked. Timestamp values use RFC 3339 JSON strings.
Neither metadata reads nor updates return a plaintext Secret or its verification
digest. A detail update changes only name and description: it does not reissue
or revoke the credential, change its creation or revocation timestamp, or modify
the App. Plaintext is returned only by creation or reset. Planned rotation
creates a new credential, allows the product to switch, then revokes the previous
one.
Multiple unrevoked credentials may coexist. Clients send no request body to
reset. The service atomically revokes the selected active Secret and creates a
replacement with a new ID and independently generated key. Its name and
description are copied from the selected record within that transaction. The old
ID remains in metadata as revoked; subsequent authentication with its key fails.
Other active Secrets remain valid. An authentication request admitted before
reset commits may finish under its earlier snapshot. Disabling an App blocks
authorization and retrieval; deletion also removes its credential records.

Resetting a missing or different App's Secret returns `404 not_found`. Resetting
an already revoked Secret returns `409 already_revoked`; concurrent resets of
the same ID cannot issue a second replacement. The new plaintext cannot be
retrieved after the response. If the response is lost after commit, the operator
must create another Secret and update the product; retrying the old ID does not
recover the replacement.

The Console collects initial Secret metadata alongside App configuration. For
later credentials it provides separate creation and detail-editing forms. App
secrets has an `Active` tab for unrevoked records and a `Disabled` tab for revoked
records, with counts and separate empty states. Opening an App selects `Active`;
new issuance returns to that tab. Both views show name, description, ID, creation
time, and revocation status, and permit metadata editing. Reset and revocation
remain available only for active records. Tab selection supports arrow keys,
Home, and End and waits for owned dialogs to finish or close.

Revocation and reset confirmations identify the named credential. Reset requires
explicit confirmation that its current key stops working immediately. One-time
plaintext displays include credential metadata so the operator can save the value
with its purpose; later detail edits cannot recover it.

Invalid input returns `400 invalid_input`, unsupported content type returns
`415 invalid_content_type`, missing records return `404 not_found`, unsupported
methods return `405 method_not_allowed`, and storage failures return
`500 internal_error`. Provider availability describes deployment configuration;
an unavailable provider cannot start authentication even if listed on an App.

The Console UI serializes configuration editing, enable/disable changes, App
deletion, and Secret creation, detail updates, reset, and revocation for the same
App.
Opening an editor or confirmation reserves that App; conflicting controls remain
unavailable through completion or cancellation. A new plaintext Secret retains
ownership until the operator marks the value saved and closes the dialog. A
pending configuration update shows `Updating…`, then adopts the returned App
object before another action can start. Opening the next editor uses that latest
snapshot, so saving a name after a completed disable retains the disabled state.
Repeated submit attempts do not start a second mutation.

After a non-authentication configuration failure, the UI retains ownership while
reloading server configuration and merges only the affected App into its current
snapshot. If the reload also fails, `State unavailable` blocks new configuration
actions until an explicit Refresh succeeds. An already-open editor or delete
confirmation still owns the App and permits Retry or Cancel. A successful owned
retry may leave the unavailable marker until Refresh. A session-invalidating
`401` returns to login and discards idle drafts, while already in-flight
operations retain their owners until their handlers settle. If pending App or
Secret issuance succeeds, its one-time plaintext display remains available
through the Saved confirmation and Done action; a failed pending operation
releases its retained owner. Another request's `401` does not establish that an
already-authorized issuance failed. These controls protect one Console instance;
API callers and separate browsers still send full App replacements without
server-side version checks.

Management mutations that pass Origin, Console session, and CSRF checks
receive `X-Operation-ID`. Their completion emits a `console management operation`
log with `operation_id`, `request_id`, `action`, `actor_account_id`,
`affected_app_id`,
`phase=completed`, `outcome`, `error_category`, and HTTP `status`. Actions are
`app.create`, `app.update`, `app.delete`, `secret.create`, `secret.update`,
`secret.reset`, and `secret.revoke`.
Creation events use the generated App ID; existing-App events use a syntactically
validated App ID. Invalid input or storage failures are recorded as failed
operations. Logs exclude credentials, App display data, and Secret names and
descriptions. `actor_account_id` is the opaque platform account ID, not an
upstream identity ID or email. Authorization failures remain observable in HTTP
request logs and
do not invoke the management handler.

## Health

`GET /healthz` returns `200 {"status":"ok"}`. `GET /readyz` also verifies a
database ping and returns `503` if that fails. These endpoints do not
perform an interactive upstream login or prove provider credentials valid.
