# UniAuth JavaScript SDK

`@codetreker/uniauth` is an ESM package for confidential product backends running
Node.js 24 or later. It has no runtime dependencies. Keep the App Secret on the
backend; this package is not a browser login-session manager.

Build and pack the source package from `sdk/js`, then install that tarball in the
product backend. This checkout does not require a published npm release.

```sh
# In sdk/js, after installing its development dependencies:
pnpm pack
# In the product backend:
pnpm add /path/to/codetreker-uniauth-0.1.0.tgz
```

Products own user records, account linking, authorization, browser-bound pending
login state, and login sessions. UniAuth returns an identity once; it does not
create the product's user or session.

## Start login and handle the callback

```ts
import {
  CallbackError,
  createClient,
  generateState,
  parseCallback,
} from "@codetreker/uniauth";

const client = createClient({
  baseURL: "https://auth.example",
  appID: process.env.UNIAUTH_APP_ID!,
  appSecret: process.env.UNIAUTH_APP_SECRET!,
});

const startLogin = async (request: ProductRequest): Promise<string> => {
  const state = generateState();
  await pendingLogins.insert({
    browser: request.browserSessionID,
    state,
    expiresAt: Date.now() + 10 * 60_000,
  });
  return client.authorizationURL({
    provider: "github",
    redirectURI: "https://product.example/auth/callback",
    state,
  });
};

const finishLogin = async (request: ProductRequest): Promise<void> => {
  const query = new URL(request.url).searchParams;
  const states = query.getAll("state");
  if (states.length !== 1) throw new CallbackError("invalid_callback");

  // This product operation atomically matches and deletes an unexpired state
  // for this browser. A different browser or a replay must receive no match.
  const pending = await pendingLogins.validateAndConsume({
    browser: request.browserSessionID,
    state: states[0],
    now: Date.now(),
  });
  if (pending === null) throw new CallbackError("state_mismatch");

  // State has already been consumed on success and provider-error callbacks.
  const code = parseCallback(query, pending.state);
  const identity = await client.retrieve(code, { signal: request.signal });
  const user = await productUsers.findOrCreateByIdentityID(identity.id);
  await productSessions.create(request.browserSessionID, user.id);
};
```

`ProductRequest`, `pendingLogins`, `productUsers`, and `productSessions` are product
code. A trusted browser session identifies the initiating browser independently
of callback query parameters. `validateAndConsume` must validate browser binding,
state equality, expiry, and one-time ownership in one atomic operation before
`retrieve`. A mismatch or replay must never trigger retrieval. Products consume
the matching pending state even when the callback reports provider failure. Use
a fresh login flow after any failure.

`generateState()` produces 32 cryptographically random bytes encoded as unpadded
Base64url. `parseCallback(query, expectedState)` validates decoded
`URLSearchParams`: exactly one nonempty state of at most 2048 UTF-8 bytes, then
state equality, then exactly one nonempty code of at most 512 UTF-8 bytes or one
recognized error of at most 256 UTF-8 bytes. State mismatch takes precedence over
provider failure and malformed code/error fields. A code and error together,
duplicates, absent values, and unknown callback errors fail validation. The
recognized provider errors are `provider_denied`, `provider_failed`, and
`capacity_exceeded`. No SDK operation stores pending states.

## Client configuration and retrieval

`createClient({ baseURL, appID, appSecret, allowLoopbackHTTP?, fetch?, timeoutMs? })`
validates configuration before use. `baseURL` must be an HTTPS origin without
credentials, a query, a fragment, or a non-root path. `allowLoopbackHTTP: true`
permits HTTP gateway URLs only for `localhost`, IPv4 loopback addresses, IPv6
`::1`, and IPv4-mapped loopback addresses; the service must also enable local
HTTP. App ID accepts 1–128 UTF-8 bytes and excludes colons. App Secret accepts
1–512 UTF-8 bytes. String inputs reject lone UTF-16 surrogates rather than
replacing them.

`client.authorizationURL({ provider, redirectURI, state })` accepts `microsoft`,
`google`, or `github`. The callback must match the registered string exactly,
contain 1–2048 UTF-8 bytes, use HTTP or HTTPS without transport opt-in, and
exclude user information, fragments, and reserved query parameters `code`,
`state`, and `error`. Its original spelling is preserved as the `redirect_uri` query value,
including casing, percent escapes, explicit ports, and existing query values.

`client.retrieve(code, { signal? })` sends exactly one Basic-authenticated JSON
`POST /auth/result`, with redirects disabled. It adds no idempotency headers and
performs no automatic retries. `fetch` selects a caller-supplied transport;
otherwise the client uses Node's native fetch. The client retains that transport
for its lifetime. Network errors, response-body read errors, and abort rejection
objects propagate unchanged, including their causes.

`timeoutMs` defaults to 30,000 and must be an integer from 1 through 2,147,483,647.
Its timeout applies even when a caller signal is supplied: either signal can
cancel the request, without aborting the caller's controller. There is no fixed
response-size ceiling; a Microsoft photo can make the complete identity larger
than 8 MiB.

Results live for five minutes in UniAuth process memory and disappear on restart.
UniAuth consumes a valid result before sending the response. If the connection
fails after consumption, retrying cannot recover that identity. Start a fresh
login after an unavailable result or an uncertain retrieval outcome.

## Identity and errors

```ts
import type { Identity } from "@codetreker/uniauth";

const useIdentity = (identity: Identity): void => {
  console.log(identity.provider, identity.id);
  // Parse this exact text with a chosen numeric representation when needed.
  persistIdentityJSON(identity.rawJSON);
  inspectProviderOrigin(identity.origin);
};
```

`Identity` includes every normalized wire field and the original response text:

| Field            | Type                                  |
| ---------------- | ------------------------------------- |
| `provider`       | `"microsoft" \| "google" \| "github"` |
| `id`             | Nonempty `string`                     |
| `email`          | `string \| null`                      |
| `email_verified` | `boolean \| null`                     |
| `name`           | `string \| null`                      |
| `avatar_url`     | `string \| null`                      |
| `origin`         | `Record<string, unknown>`             |
| `rawJSON`        | `string`                              |

All seven wire fields must be present with these types. Missing or invalid fields
raise `ProtocolError`; explicit `null` and `false` remain unchanged. The SDK
copies the service's normalized values verbatim, without deriving an ID or
profile from `origin`. Products own account-linking and email-verification policy.

All unknown fields, nulls, false values, arrays, and nested objects inside
`origin` remain available. `rawJSON` is the complete exact UTF-8 JSON response,
including whitespace and unknown envelope fields; it is authoritative when
numeric fidelity matters. `origin` is a native JavaScript convenience view:
JSON numbers use ordinary `number` precision, so an integer such as
`9007199254740993` may round. Treat identity data as sensitive and avoid logging
it indiscriminately.

| Error                        | Meaning                                                                                                                                                                                        |
| ---------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TypeError`                  | Invalid SDK configuration or outbound input. Messages omit supplied values.                                                                                                                    |
| `CallbackError`              | `code` is `invalid_callback`, `state_mismatch`, or one of the three provider callback errors.                                                                                                  |
| `APIError`                   | A structured service error on HTTP 4xx/5xx, with `status`, `code`, `message`, and `requestID` (`null` if absent). Unknown future service codes remain available.                               |
| `ProtocolError`              | Invalid JSON, content type, identity/error envelope, redirect, or unexpected HTTP status, with `status` and `requestID`. Diagnostics exclude result codes, credentials, and raw identity JSON. |
| Original transport rejection | Network failure or cancellation; the original error object and cause are preserved.                                                                                                            |

## Development

```sh
pnpm install --frozen-lockfile
pnpm check
pnpm test
pnpm format:check
pnpm test:package
```

`test:package` packs the actual tarball, installs it without scripts into a fresh
repository-local `.tmp/` consumer using the repository's offline pnpm store,
checks strict TypeScript `NodeNext` declarations with `skipLibCheck: false`, and
runs an ESM import by package name. The consumer installs only the SDK tarball,
whose package has no runtime dependencies. Type checking uses the SDK's pinned
compiler and Node ambient types from frozen dependency setup; SDK imports still
resolve from the consumer to the installed tarball, without source aliases.
This avoids registry metadata resolution for a second compiler installation.
Only the temporary consumer permits lockfile generation for its local tarball;
repository dependency setup remains frozen. Temporary consumer files are
removed on both success and failure. Install the SDK development dependencies
before running this offline check.

The [service API reference](../../docs/reference/api.md) owns the downstream HTTP
protocol and source-specific identity contracts.
