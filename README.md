# UniAuth

UniAuth is a Go authentication gateway for product backends. It supports personal
Microsoft accounts, Google, and GitHub, with a public Console for application
registration and Secret management. Anyone can sign in through a configured OAuth
provider and manage their own Apps. Products own their users, account linking,
permissions, and login sessions.

SQLite stores Console platform accounts, explicitly linked identities, owned App
configuration, and credential digests. The gateway stores no external product
users or central SSO session. Completed identity results exist only in process
memory for five minutes and can be retrieved once by their owning App. The Console
is itself a protected UniAuth App and uses this same handover.

## Build and run

Install the Go version declared in [go.mod](go.mod), Node.js 24 or later, and the
pnpm version pinned in [package.json](package.json).
Python 3 is used by the documentation check.

```sh
make setup
make build
```

Set the initial administrator email and at least one provider credential pair in
deployment configuration. For local development:

```sh
export UNIAUTH_PUBLIC_URL=http://127.0.0.1:8080
export UNIAUTH_ALLOW_LOOPBACK_HTTP=true
export UNIAUTH_DATABASE_PATH=./data/uniauth.db
export UNIAUTH_INITIAL_ADMIN_EMAIL=sychenjianjun@gmail.com
export UNIAUTH_GOOGLE_CLIENT_ID=YOUR_GOOGLE_CLIENT_ID
export UNIAUTH_GOOGLE_CLIENT_SECRET=YOUR_GOOGLE_CLIENT_SECRET
mkdir -p data
.tmp/bin/uniauth serve
```

Open `/console/` and choose any configured provider. Accounts are identified by
stable provider IDs; matching email addresses never merge accounts. After signing
in, explicitly link additional login identities from the account controls.

The first Google or GitHub identity with a verified email matching
`UNIAUTH_INITIAL_ADMIN_EMAIL` claims administrator authority. Microsoft can sign
in and link, but its unknown email verification cannot claim that role. The
administrator sees minimal user summaries and App counts; each user, including
the administrator, manages only their own Apps. Password login is removed.

Each upstream provider requires its own Client ID and Client Secret pair.
Production requires HTTPS, normally terminated by a reverse proxy in front of
the service. See the [configuration reference](docs/reference/configuration.md)
for environment variables, bootstrap rules, and provider registration callbacks.

Database migration 3 clears all existing Apps and Secrets when upgrading to the
platform schema. Recreate product registrations and replace their backend
credentials after upgrading. Platform accounts and newly created Apps then persist
across restarts; transient logins, results, and Console sessions do not.

## Release with Docker Compose

Start the Release workflow manually on `main` from GitHub Actions or with
`gh workflow run release.yml --ref main`. Its Actions run title includes the
run number, deployment flow, branch, initiating actor, and full Git commit hash:
`Release #<run-number> · staging → prod · <branch> · @<actor> · <commit>`.
It publishes to
`ghcr.io/codetreker/uniauth` with tags in `<yymmdd>-<seq>-<hash-8>` format, such
as `261013-3-a1b2c3d4`. The allocation step uses the current UTC date, the next
sequence from that date's published registry tags, and the first eight source
commit characters. Anonymous pull verification and both staging and prod use
that same tag reference. The workflow copies `deploy/docker-compose.yml` to each
deployment directory and runs `docker compose up -d` there. The prod container
is named `uniauth`, and the staging container is named `uniauth-staging`.
The VM owns `.env` and Caddy configuration; public images need no registry login.
See the [deployment reference](docs/reference/deployment.md) for tag allocation, GitHub
settings, the one-time Public package setting, and VM prerequisites.

## Connect a product

Create an App in the Console, register its exact HTTP or HTTPS callback URLs,
and select allowed providers. Creating an initial Secret is optional; you can
create one later in the App's Secrets section. Each Secret has a name and an
optional description for its purpose or rotation notes. Save its one-time value
in the product backend before retrieving identities. The Console groups Secrets
into `Active` and `Disabled` tabs, with revoked Secrets in `Disabled`. Both views
let you edit their details without changing credential validity. Start a browser login at
`/auth/authorize` with `app_id`, `provider`, `redirect_uri`, and a random `state`
bound to that browser's pending product login.

For planned credential rotation, create a second Secret, update the product,
then revoke the previous one. Reset an active Secret when its current key must
stop working immediately. The replacement keeps its name and description. The
Console asks for confirmation and shows the new key once; save it and update the
product.

The product callback receives `code` and the original `state`. Validate and
consume the pending product login before the backend sends an authenticated
`POST /auth/result` with `{"code":"RECEIVED_CODE"}`. Use HTTP Basic authentication
with App ID and Secret. Every successful response contains `provider`, a stable
provider-qualified `id`, standard `email`, `email_verified`, `name`, and
`avatar_url` fields, and complete provider source objects under `origin`.
Unavailable standard profile fields are `null`; an explicit verification value
of `false` remains `false`. Products control account linking and email policy.

Product backends can use the [Go SDK](sdk/go/README.md) or
[JavaScript SDK](sdk/js/README.md) to create authorization URLs, validate callback
parameters, and retrieve complete identity results. The JavaScript package emits
ESM and TypeScript declarations for Node.js 24 or later. Both use the existing
HTTP protocol, expose the server's seven-field normalized identity and complete
`origin`, and leave pending state, users, and sessions with the product. Missing
standard fields or incompatible response types are SDK protocol errors.

Keep Secrets and result retrieval server-side. Atomically validate and consume the
browser-bound pending login state before the one-time result request. A lost,
aborted, or malformed retrieval response may already have consumed the result;
start a fresh login rather than retrying the code. See the
[SDK reference](docs/reference/sdks.md) for API examples and local package use,
and the [API reference](docs/reference/api.md) for the underlying protocol.
Package metadata and local artifact validation do not establish a registry
release; this delivery does not publish a Go tag or JavaScript package.

## Development

| Command | Purpose |
| --- | --- |
| `make setup` | Install pinned Go and pnpm dependencies using repository-local caches. |
| `make build` | Build Console assets, both SDKs, and the Go executable in `.tmp/bin/`. |
| `make sdks` | Build the standalone Go module and emitted JavaScript/types. |
| `make check` | Check documentation, formatting, Go vet for both modules, and Console/SDK types and formatting. |
| `make test` | Enforce Go coverage/race gates, test release tag allocation and Console/JavaScript behavior, and validate package consumers plus both SDKs against the gateway. |
| `make test-release` | Test UTC daily image tag allocation and registry failure handling with controlled fixtures. |
| `make coverage` | Run the authoritative go-cov policy for the service and standalone Go SDK. |
| `make browser` | Build and run Playwright Console and mockup scenarios. |
| `make mockup` | Open the interactive Console preview using production components and styles. |

The mockup uses production components and styles with representative accounts,
owned Apps, named active and revoked Secrets, and multiline descriptions. Its
OAuth choices and account controls operate on local fixtures; state resets on
reload and fixtures are excluded from the production build. Browser tests may use an
existing Chromium executable through `PLAYWRIGHT_CHROMIUM_EXECUTABLE`; temporary
browser installs belong under `.tmp/`.

The [documentation map](docs/README.md) links requirements, implemented behavior,
decisions, and validation evidence. All changes use task worktrees under
`.worktrees/`, as described in [AGENTS.md](AGENTS.md).
