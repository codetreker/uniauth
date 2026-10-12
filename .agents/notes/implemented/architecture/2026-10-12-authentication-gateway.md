# Agent Note: Authentication Gateway Ownership and Handover

Status: implemented

## Problem

Several products need external account authentication without each implementing
Microsoft, Google, and GitHub identity verification and maintaining provider
credentials. They need complete provider identity information while retaining
control of their own user records, account linking, permissions, and sessions.
Product registration and credential administration must remain independent of
end-user authentication.

## Decision

UniAuth is a Go gateway with an embedded React and TypeScript administrator
Console. SQLite persists App definitions and verification digests of product
Secrets. Upstream credentials and the Console administrator Argon2id password
hash come from deployment configuration. No end-user table or central SSO
session is created. The Console has its own eight-hour in-memory session and
grants no authority to product users.

Each login has separate product and provider state, browser binding, PKCE, and
OIDC nonce where applicable. The gateway validates the provider identity and
collects complete source JSON under the granted scopes. Microsoft supports only
personal accounts and uses the personal tenant plus `oid`; Google uses `sub`;
GitHub uses its authenticated numeric user ID. Qualified subjects keep provider
identities distinct. Each product controls cross-provider account linking.

The callback carries a random result code and the product's original state.
The product validates that state, then its backend authenticates using App ID
and Secret to retrieve identity JSON. The complete payload is serialized before
insertion into memory, bound to the App, and expires five minutes after creation.
Ownership and expiry checks and deletion occur atomically; one claim succeeds.
Authentication failures and wrong-App claims do not consume another App's result.

Unknown JSON fields, explicit nulls, false booleans, complete email arrays, and
binary Microsoft photo data retain their source meaning. Tokens and other
authentication credentials are excluded. Missing required identity keys and
upstream retrieval failures are errors, with no identity-key fallback or empty
profile substitution.

The [blueprint](../../../../docs/blueprint/README.md) owns the architectural
requirements. [API](../../../../docs/reference/api.md) and
[configuration](../../../../docs/reference/configuration.md) references own the
implemented requests, identity sources, limits, and deployment behavior.

## Alternatives

**Unified users and central SSO sessions.** A shared user store and session could
coordinate identity and login across products. The agreed ownership leaves user
records, account linking, permissions, and sessions with each product, so the
gateway has no authority or persistence for those responsibilities.

**Encrypted identity in callback parameters.** Encrypting the complete identity
package with the App Secret and Base64 encoding it was considered, followed by
standard JWE authenticated encryption. It required shared-key derivation,
encryption metadata, rotation handling, and replay checks while still increasing
URL size for complete profiles. Backend retrieval supplies the package over an
authenticated HTTP response and keeps it out of browser callback parameters.

**Durable handover state.** Persisted results would survive restart and permit
different recovery policies. The selected design explicitly retains completed
results only in memory for five minutes and deletes them on the first accepted
claim. A response lost after deletion and a restart require a fresh login.
Durability would change the retention and consumption contract and requires a
new decision.

**PostgreSQL application configuration.** PostgreSQL could support a service
with different database deployment requirements. SQLite was chosen for one
process with a small configuration registry and fewer deployment dependencies.
Multiple UniAuth replicas would still need a separate decision for transient
authentication state; changing the durable configuration database alone would
not establish that capability.

## Consequences

Products integrate one login-and-retrieval protocol and receive complete
provider-specific data without managing upstream OAuth credentials. Product
Secrets remain server-side, are shown at creation only, and can overlap during
rotation before old credentials are revoked.

Transient identity state has bounded count, accounted bytes, and lifetime.
No long-lived recovery, consumed-code history, token refresh, or central logout
is provided. All requests in a login must reach the same process. Restart also
invalidates Console sessions while retaining App configuration.

Provider fields and consent determine the available profile data. Product email
or permission policy cannot be inferred from authentication success. Microsoft
photo `404` is a documented absence; other upstream errors stop authentication.
Changing a stable subject convention requires an explicit product identity
migration.

The executable embeds the production Console build. Go, Node.js, and pnpm are
build dependencies; only the resulting executable and durable SQLite files are
needed to serve the application. Interactive upstream login still needs valid
provider registrations and post-deployment verification. Automated fixture tests
verify the local protocol and validation behavior; they do not establish live
provider consent or account exchange.
