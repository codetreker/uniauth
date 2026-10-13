# UniAuth

UniAuth is a Go authentication gateway for product backends. It supports personal
Microsoft accounts, Google, and GitHub, with an embedded administrator Console
for application registration and Secret management. Products own their users,
account linking, permissions, and login sessions.

UniAuth stores application configuration in SQLite. Completed identity results
exist only in process memory for five minutes and can be retrieved once by the
owning application. There is no end-user database or central SSO session.

## Build and run

Install the Go version declared in [go.mod](go.mod), Node.js 24 or later, and the
pnpm version pinned in [package.json](package.json).
Python 3 is used by the documentation check.

```sh
make setup
make build
.tmp/bin/uniauth hash-password
```

The password command reads stdin. On a terminal it hides input; its output is an
Argon2id PHC hash. It works independently of service configuration and accepts no
password argument. Put the generated hash and provider credentials in deployment
configuration. For local development:

```sh
export UNIAUTH_PUBLIC_URL=http://127.0.0.1:8080
export UNIAUTH_ALLOW_LOOPBACK_HTTP=true
export UNIAUTH_DATABASE_PATH=./data/uniauth.db
export UNIAUTH_ADMIN_PASSWORD_HASH='YOUR_GENERATED_ARGON2ID_PHC_HASH'
mkdir -p data
.tmp/bin/uniauth serve
```

Open `/console/` and sign in with the password used to generate the hash.
Configure providers before creating usable integrations; each provider requires
its own Client ID and Client Secret pair. Production requires HTTPS, normally
terminated by a reverse proxy in front of the service. See the
[configuration reference](docs/reference/configuration.md) for environment
variables and provider registration callbacks.

## Release with Docker Compose

Start the Release workflow manually on `main` from GitHub Actions or with
`gh workflow run release.yml --ref main`. It publishes to
`ghcr.io/codetreker/uniauth` and verifies anonymous pull before copying
`deploy/docker-compose.yml` to staging and prod. It runs `docker compose up -d`
in each deployment directory using the same image digest. The VM owns `.env`
and Caddy configuration; public images need no registry login. See the
[deployment reference](docs/reference/deployment.md) for GitHub settings, the
one-time Public package setting, and VM prerequisites.

## Connect a product

Create an App in the Console and save its Secret in the product backend. Register
the exact callback URL and select allowed providers. Start a browser login at
`/auth/authorize` with `app_id`, `provider`, `redirect_uri`, and a random `state`
bound to that browser's pending product login.

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
| `make test` | Enforce both Go coverage/race gates, run Console/JavaScript tests, and validate package consumers plus both SDKs against the gateway. |
| `make coverage` | Run the authoritative go-cov policy for the service and standalone Go SDK. |
| `make browser` | Build and run Playwright Console and mockup scenarios. |
| `make mockup` | Open the interactive Console preview using production components and styles. |

The mockup starts authenticated with representative fixture applications. After
signing out, use the fixture password `preview`. Its state resets on reload and
its fixtures are excluded from the production build. Browser tests may use an
existing Chromium executable through `PLAYWRIGHT_CHROMIUM_EXECUTABLE`; temporary
browser installs belong under `.tmp/`.

The [documentation map](docs/README.md) links requirements, implemented behavior,
decisions, and validation evidence. All changes use task worktrees under
`.worktrees/`, as described in [AGENTS.md](AGENTS.md).
