# UniAuth Blueprint

- Status: Agreed product and architecture direction
- Date: 2026-10-12
- Backend language: Go

## Purpose and ownership

UniAuth is the shared authentication gateway for our products. It authenticates an external account through Microsoft, Google, or GitHub and hands the complete identity result to the requesting product backend.

UniAuth owns third-party authentication, product application registration, and short-lived authentication transactions. Each product owns its user records, account linking, business permissions, and login sessions. Authenticating through UniAuth does not grant access to product resources or permission to administer UniAuth.

UniAuth has no persistent end-user database, user profile store, cross-provider account-linking table, or central SSO session. Identity information is processed during authentication and retained only in the temporary in-memory result described below.

## Requirements

| ID | Required behavior |
| --- | --- |
| R1 | Implement the service backend and Console management API in Go. |
| R2 | Support personal Microsoft accounts, Google accounts, and GitHub accounts. |
| R3 | Provide an administrator Console that creates and manages product applications and generates App IDs and Secrets. |
| R4 | Preserve and hand over all identity information obtained from the selected provider under the granted scopes. |
| R5 | Keep completed identity results exclusively in process memory for five minutes from result creation. |
| R6 | Delete a valid result atomically when an authenticated product backend claims it; only one claim can succeed. |
| R7 | Keep product user records, account linking, authorization, and sessions within each product. |
| R8 | Use a browser callback carrying a one-time result code, followed by a backend request that retrieves the full identity result. |

## Architecture

```mermaid
flowchart LR
    Browser[User browser] -->|Start product login| Product[Product backend]
    Product -->|Redirect with app, provider, callback and state| Gateway[UniAuth Go service]
    Gateway --> Microsoft[Microsoft personal accounts]
    Gateway --> Google[Google]
    Gateway --> GitHub[GitHub]
    Gateway -->|Complete identity result, five-minute lifetime| Cache[In-process result cache]
    Gateway -->|Redirect browser with code and state| Product
    Product -->|Authenticated result retrieval| Gateway
    Cache -->|Atomic take and delete| Gateway
    Gateway -->|Complete identity JSON| Product
    Console[Administrator Console] -->|Manage applications| Config[Durable application configuration]
    Config --> Gateway
```

The downstream product integration is a small HTTP login-and-retrieval protocol. Microsoft and Google use OIDC upstream; GitHub uses OAuth authorization code flow followed by authenticated identity API requests. A product receives identity JSON and establishes its own session.

## Application registry and Console

The Console manages applications with the following configuration:

| Field | Meaning |
| --- | --- |
| App ID | Public identifier for a registered product integration. |
| Name | Human-readable application name. |
| Secret | High-entropy credential held by the product backend and used to authenticate result retrieval. |
| Callback URLs | Explicitly registered callback URLs, matched exactly against login requests. |
| Allowed providers | The subset of Microsoft, Google, and GitHub that the application may use. |
| Enabled state | Whether the application may start authentication and retrieve results. |

Separate production and non-production integrations use separate registrations and credentials.

Generate Secrets with a cryptographically secure random source. Reveal a newly generated Secret once, retain only its verification hash, and compare credentials without timing-dependent comparisons. Support Secret rotation and revocation. Upstream provider credentials are separate configuration: UniAuth needs recoverable access to credentials used for provider token exchanges, so their storage has different requirements from downstream Secret verification hashes.

The integration requires a confidential product backend. Its Secret remains server-side. The Console uses an administrator password supplied through deployment configuration, with an Argon2id verification hash. Its administrator session is separate from product authentication and is held in process memory. There is no administrator user table. A local command generates the deployment password hash without including the password in command-line arguments.

SQLite persists application configuration and Secret verification records. Upstream provider credentials come from deployment configuration. These configuration records survive process restarts and do not constitute an end-user database.

The Console frontend uses React, TypeScript, Vite, and pnpm. Its production static assets are embedded in the Go executable and served by the same process under `/console/`. Node.js is a build dependency. Interactive mockups use the production components and styles with representative fixture data; mockup fixtures are absent from production assets.

## Authentication and retrieval flow

1. The product creates an unpredictable `state`, binds it to the initiating browser's pending login, and redirects the browser to UniAuth with its App ID, selected provider, registered callback URL, and `state`.
2. UniAuth validates the enabled application, allowed provider, and exact callback URL. It creates a separate transient provider-authentication transaction. Provider `state`, OIDC `nonce`, and PKCE values belong to that transaction; the product's `state` is preserved independently.
3. UniAuth completes the provider authorization-code exchange, validates the provider identity, and obtains the identity sources required by the configured scopes.
4. UniAuth prepares an immutable, serializable identity result. It generates an unpredictable one-time result code, binds the result to the requesting App ID and approved callback, and inserts it into the in-process cache. Its deadline is five minutes after the complete result is created.
5. UniAuth redirects the browser to the approved product callback with `code` and the original product `state`.
6. The product verifies `state` against the pending login associated with that browser before requesting the result.
7. The product backend authenticates to UniAuth with App ID and Secret and submits the result code.
8. UniAuth authenticates the enabled application before consuming anything. It atomically checks result ownership and expiry, removes a valid result, and returns its complete identity JSON.
9. The product stores the identity information it needs, performs its own user and permission processing, and creates its own session.

Illustrative endpoints:

```text
GET /auth/authorize?app_id=app_example&provider=github&redirect_uri=https%3A%2F%2Fproduct.example%2Fauth%2Fcallback&state=PRODUCT_STATE

GET https://product.example/auth/callback?code=RESULT_CODE&state=PRODUCT_STATE

POST /auth/result
Authorization: Basic BASE64_APP_ID_COLON_SECRET
Content-Type: application/json

{"code":"RESULT_CODE"}
```

Production traffic uses HTTPS. Credentials are carried in the authorization header, and the callback contains no identity profile data. Authentication results and retrieval responses are not cacheable. Application and callback validation occurs before issuing a provider redirect or a result code.

## Complete identity handover

The result contains the provider, a stable provider-qualified subject, and source-specific identity objects. Original source fields remain nested under their source names so that similarly named fields from different sources retain their meaning.

An illustrative envelope follows. Actual source objects retain every obtained field, not just the example fields:

```json
{
  "provider": "github",
  "subject": "github:123456",
  "sources": {
    "profile": {
      "id": 123456,
      "login": "example",
      "name": "Example",
      "email": null
    },
    "emails": [
      {
        "email": "example@example.com",
        "primary": true,
        "verified": true,
        "visibility": "private"
      }
    ]
  }
}
```

Preserve complete successfully obtained ID-token claims, UserInfo objects, provider profile objects, and email arrays, as applicable. For binary identity data obtained from a provider, preserve the content type and bytes using a documented encoding in the source object. Go implementations can retain source JSON using `json.RawMessage` to preserve fields that are not represented by common-profile structs.

Completeness means all identity data obtained under the configured and granted scopes. Providers can withhold fields or return explicit null values. Preserve absent fields, nulls, and boolean values accurately. Missing email-verification information remains unknown. Provider API failures are reported as errors rather than fabricated empty profile data. Upstream access tokens, refresh tokens, authorization codes, and Secrets are authentication credentials and are outside the identity-data envelope.

### Provider contracts

| Provider | Identity validation and sources | Stable subject contract |
| --- | --- | --- |
| Microsoft | Register for personal Microsoft accounts only and use the `consumers` authority. Validate the ID token and its signature, issuer, audience, expiry, and nonce. Login scopes include `openid profile email`; additional Graph profile or photo access requires explicit permissions. | `microsoft:<validated tid>:<validated oid>`. Require the personal-account tenant and `oid`; do not silently substitute another identifier. |
| Google | Validate the OIDC ID token. When UserInfo is fetched, verify that its `sub` matches the ID token. Preserve the complete claims and obtained UserInfo. | `google:<validated sub>`. |
| GitHub | Complete OAuth authorization-code flow and retrieve the authenticated user. Use the appropriate profile and email scopes when obtaining `/user` and `/user/emails`. | `github:<authenticated user id>`. |

The Microsoft personal-account tenant is `9188040d-6c67-4c5b-b112-36a304b66dad`. Its registration audience is `PersonalMicrosoftAccount`, with authority `https://login.microsoftonline.com/consumers/v2.0`.

Subject generation is fixed and independent of App Secrets. Emails and usernames are profile attributes, not identity keys. Different provider accounts produce different subjects; each product decides whether to link them. Changing the subject contract requires an explicit product identity migration.

## In-memory result lifecycle

Each pending result holds its owning App ID, approved callback binding, immutable identity payload, and expiry deadline. Codes are generated using a cryptographically secure random source and do not contain identity information.

```text
complete provider identity
          |
          v
pending result ---- five-minute deadline ----> expired and removed
          |
 authenticated, owning app claims result before expiry
          |
          v
atomically removed; identity response emitted once
```

Implement result claiming as one atomic operation. For a mutex-protected Go map, ownership and deadline checks and deletion occur within the same critical section. Authenticate the application before entering that operation. Prepare the response payload before caching it so serialization errors cannot consume a result.

Required semantics:

- Exactly one concurrent valid retrieval succeeds.
- Failed authentication and another application's request do not consume a live result.
- Expiry is checked on every retrieval, independently of cleanup timing. A result is expired at or after its deadline.
- A periodic cleanup removes abandoned expired results.
- Cache capacity is bounded by entry count and retained bytes. Capacity exhaustion produces an explicit failure before publishing a code; active accepted results are not silently evicted or truncated.
- Expired, already consumed, unknown, and unowned codes produce an explicit unavailable-result error. Their detailed history is not retained or exposed.

For example, the retrieval API returns `401` for failed application authentication, `400` for a malformed request, and `410` with a result-unavailable error for a code that the authenticated application cannot claim. Exact machine-readable error names are part of the implementation contract.

Successful retrieval means UniAuth accepts the authenticated claim and removes the result before writing the response. It cannot prove that the product received the HTTP response. A response lost after consumption requires a fresh login; the same result code cannot be retried successfully.

## Deployment and failure behavior

This architecture uses one UniAuth process. A process restart loses outstanding provider transactions and pending results, while durable application configuration remains available. Affected products start a fresh login.

Authentication, provider callbacks, and result retrieval must reach the process holding their transient state. Multiple replicas require an explicit routing or state-sharing design before they are supported.

On shutdown, stop admitting new work, drain active HTTP requests within a bounded deadline, stop cleanup work, and release transient memory. Provider timeouts, validation failures, result expiry, capacity exhaustion, and response-delivery failures remain visible errors.

## Observability and administration

Use structured, sanitized logs for authentication and result retrieval. Establish an internal login operation ID when accepting an authentication request and propagate it through provider authentication, result creation, and retrieval. Each HTTP request has its own request ID. Available correlation fields include operation ID, request ID, App ID, provider, component, and operation phase.

Do not log identity payloads, email addresses, provider account identifiers, result codes, product or provider `state`, authorization headers, Secrets, or upstream tokens. Avoid logging full callback or provider URLs containing sensitive query parameters. Errors include their category, sanitized cause, component, phase, and available correlation fields.

The result cleanup task has service-owned lifecycle and cancellation. It reports aggregate expiry and cache-capacity metrics without retaining expired identity data. Provider calls inherit the request or authentication transaction context and have bounded deadlines; cleanup uses its own lifecycle context.

Post-deployment verification: filter structured application logs by `operation_id=<login operation ID>` and `app_id=<requesting app ID>`. A successful trace shows provider completion, result creation, and one accepted retrieval; the accepted request obtains its operation ID from the live result before removal. Unavailable-result requests are searchable by their current request ID and authenticated App ID. After a result has been removed, its original operation ID may no longer be available to a later retrieval. Correlation does not depend on recording the public result code, retaining code history, or recording a user identity.

Console configuration changes have management operation IDs and affected App IDs for audit correlation. Administrator authority is independent of a product login.

## Implementation acceptance criteria

- Microsoft organizational accounts are rejected; personal accounts, Google accounts, and GitHub accounts follow their respective validation rules.
- The Console creates usable App ID/Secret pairs and manages callbacks, provider eligibility, enablement, and credential lifecycle.
- Modified or mismatched provider state, OIDC nonce, issuer, audience, and UserInfo subject fail visibly.
- Product callbacks preserve the original product `state` and use only registered callback URLs.
- Every obtained identity field survives the handover, including unknown source fields, complete email lists, explicit nulls, and false verification flags.
- Results expire five minutes after creation, including when cleanup has not yet run.
- Concurrent valid retrievals yield one successful response; wrong-app and failed-credential probes leave a rightful result claimable.
- Lost-response and process-restart behavior require a new login without adding a durable result or retry store.
- Capacity limits reject new work explicitly while preserving active results.
- Consumer identities, credentials, and callback query values remain absent from persistent logs.
- Go validation covers these caller-visible properties, with go-cov used to enforce the project's coverage policy once implementation exists.

## References

- [Microsoft personal-account authority and OIDC flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-protocols-oidc)
- [Microsoft application audience configuration](https://learn.microsoft.com/en-us/entra/identity-platform/reference-app-manifest)
- [Microsoft ID-token claims and stable identifiers](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference)
- [Microsoft UserInfo identity sources](https://learn.microsoft.com/en-us/entra/identity-platform/userinfo)
- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [GitHub OAuth authorization flow](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps)
- [GitHub stable user identifiers](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/best-practices-for-creating-an-oauth-app)
- [GitHub authenticated user profile](https://docs.github.com/en/rest/users/users#get-the-authenticated-user)
- [GitHub email identity information](https://docs.github.com/en/rest/users/emails#list-email-addresses-for-the-authenticated-user)
- [OIDC UserInfo subject validation](https://openid.net/specs/openid-connect-core-1_0.html#UserInfoResponse)
- [OAuth authorization-code lifetime, binding, and single-use properties](https://www.rfc-editor.org/rfc/rfc6749#section-4.1.2)
