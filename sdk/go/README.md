# Go product SDK

`github.com/codetreker/uniauth/sdk/go` is an independent Go 1.27.1 module with no
external dependencies. It connects a confidential product backend to UniAuth.
The product owns users, account linking, authorization, browser-bound pending
login state, and login sessions. Keep App Secrets on the backend.

The module is delivered in this repository. To consume the current source from
another Go module, use a local replacement pointing to your checkout:

```go
require github.com/codetreker/uniauth/sdk/go v0.0.0

replace github.com/codetreker/uniauth/sdk/go => ../uniauth/sdk/go
```

A remote `go get` version requires a published module tag; this source delivery
does not establish a registry release.

## Start a browser login

```go
client, err := uniauth.New(uniauth.Config{
    BaseURL:   "https://auth.example",
    AppID:     appID,
    AppSecret: appSecret,
})
if err != nil {
    return err
}

state := uniauth.GenerateState()
// Store state as a pending login bound to the initiating browser, with expiry.
authorizationURL, err := client.AuthorizationURL(uniauth.AuthorizationRequest{
    Provider:    uniauth.GitHub,
    RedirectURI: "https://product.example/auth/callback?return=%2Faccount",
    State:       state,
})
if err != nil {
    return err
}
http.Redirect(w, r, authorizationURL, http.StatusFound)
```

`GenerateState` returns 32 random bytes in URL-safe Base64. Provider constants
are `Microsoft`, `Google`, and `GitHub`. `AuthorizationURL` validates provider,
state, and callback syntax, then preserves the callback string exactly as the
`redirect_uri` query value. It contains the App ID and never the App Secret.
The gateway checks App registration and provider availability.

`BaseURL` must be an HTTPS origin, optionally ending in `/`, without credentials,
query, fragment, or another path. `AllowLoopbackHTTP: true` permits HTTP gateway
URLs only for localhost or loopback IPs; the service must also enable local HTTP.

Callback URLs accept HTTP or HTTPS without transport opt-in, contain 1–2048
UTF-8 bytes, and exclude user information, fragments, and reserved query keys
`code`, `state`, and `error`. The callback must match its registered string
exactly, including scheme, spelling, explicit ports, and existing query values.

## Complete the callback

Parse the query with `url.ParseQuery(r.URL.RawQuery)` and handle its error before
calling `ParseCallback`. Locate the expected pending state through the product's
browser binding. Validate the callback and atomically consume that pending state
on success or failure **before calling `Retrieve`**. A separate lookup and delete
allows two callback requests to pass the check; use a transaction or equivalent
atomic claim. `ParseCallback` validates values but neither stores nor consumes
pending state.

```go
query, err := url.ParseQuery(r.URL.RawQuery)
if err != nil {
    return err
}
// ValidateAndConsume is a product-owned atomic operation, described below.
code, err := pendingLogins.ValidateAndConsume(r.Context(), browserBinding,
    func(expectedState string) (string, error) {
        return uniauth.ParseCallback(query, expectedState)
    },
)
if err != nil {
    return err
}
identity, err := client.Retrieve(r.Context(), code)
if err != nil {
    return err
}
// Resolve identity.Provider + identity.ID to a product account and create
// the product's own login session according to its account-linking policy.
```

The illustrative `pendingLogins.ValidateAndConsume` operation belongs to the
product. It verifies the browser binding and pending deadline, runs the callback
validator with the stored expected state, and consumes the pending login within
one atomic operation when callback state matches, including a provider failure.
Invalid or mismatched callback state does not claim the pending login. It rejects
a missing, expired, or already consumed login before returning a result code. `browserBinding`
comes from the product's trusted browser-session mechanism.

`ParseCallback` requires exactly one nonempty matching `state`, then exactly one
nonempty `code` or a documented `error`. State is limited to 2048 UTF-8 bytes and
code to 512; duplicate, empty, invalid UTF-8, and mixed code/error values fail.
`CallbackError.Code` is `invalid_callback`, `state_mismatch`, `provider_denied`,
`provider_failed`, or `capacity_exceeded`.

`Retrieve` makes one POST using HTTP Basic authentication and JSON. It never
follows redirects or retries, and sends no idempotency header. Results belong to
an App, expire five minutes after creation, disappear on service restart, and
are consumed before the gateway writes the response. A failed or lost response
requires a fresh login; retrying cannot recover a consumed identity.

## Identity and errors

Every successful identity response must include all seven gateway fields:

| Go field | Type | JSON field |
| --- | --- | --- |
| `Provider` | `Provider` | `provider` |
| `ID` | `string` | `id` |
| `Email` | `*string` | `email` |
| `EmailVerified` | `*bool` | `email_verified` |
| `Name` | `*string` | `name` |
| `AvatarURL` | `*string` | `avatar_url` |
| `Origin` | `map[string]json.RawMessage` | `origin` |

`Provider` must be supported, `ID` must be nonempty, and `Origin` must be an
object. `Email`, `EmailVerified`, `Name`, and `AvatarURL` may be JSON `null`,
represented by nil pointers. A non-null verification value remains a boolean
pointer, including explicit `false`; an unknown value remains nil. Non-null
string values remain exactly as received, including empty strings and whitespace.
Missing fields and incorrect types are protocol failures. The SDK performs no
normalization, fallback mapping, or support for `subject`/`sources` aliases.

Use the gateway's provider-qualified `ID` as the stable identity key. Email,
verification, display name, and avatar values are attributes; products choose
their own account-linking and permission policies.

`Origin` maps source names to `json.RawMessage`; arbitrary upstream fields, large
integers, decimal precision, arrays, nested objects, nulls, and false values
survive decoding. `Identity.RawJSON` retains the complete response bytes,
including unknown envelope fields. Provider source schemas are not validated or
filled with fallback values. The response has no fixed size cap; Microsoft photo
and avatar data and other complete sources may exceed 8 MiB.

Use `errors.As` to inspect `*APIError` (`StatusCode`, `Code`, `Message`,
`RequestID`) for valid JSON gateway failures, including future error codes.
`*ProtocolError` distinguishes unexpected HTTP status, content type, JSON,
identity/error schema, and body failures using `Reason`. `*RequestError` retains
transport and context failures. Both expose their underlying causes through
`errors.Is` and `errors.As`. Error strings omit request codes, credentials, and
response data.

The default HTTP client has a 30-second timeout. A supplied `HTTPClient` keeps
its transport, jar, and timeout; the SDK copies it and overrides redirect handling
on the copy. Its transport must honor context cancellation and must not implement
its own retries for one-time claims. The client can be shared concurrently when
the supplied transport and jar support concurrent use.

See the [API reference](../../docs/reference/api.md) for the gateway protocol.
From the repository root, `bash scripts/coverage.sh sdk/go` applies the shared
coverage policy; SDK tests and the race detector can also run inside this module.
