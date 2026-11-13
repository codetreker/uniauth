# Agent Note: Authentication Diagnostics and Bounded Log Files

Status: implemented

## Problem

A provider failure category identifies which authentication operation failed but
does not explain where its deadline was spent. An authenticated profile request
can fail while an unauthenticated network probe succeeds. Operators need actual
flow timings and transport observations correlated with the failed login, plus
a retained log artifact that survives container replacement.

## Decision

Provider diagnostics use the callback's existing request, operation, App, and
provider correlation through a context-bound structured logger. Adapters keep
ownership of token exchange, identity verification, and profile/photo stages;
the auth handler supplies correlation without introducing a reverse dependency.
Each observed stage records its start and completion, monotonic elapsed and
stage time, remaining deadline, and allowlisted failure metadata. HTTP traces
summarize observed DNS, connection, TLS, reuse, request writing, first byte,
headers, and body reading. Numeric remote IPs, response status, and negotiated
protocol remain observable without recording credential-bearing requests.
Unobserved timings stay absent. Shared signing-key and OIDC behavior remains
owned by its existing adapters. The diagnostics delivery preserved the
then-current authentication deadlines, identity values, error policy, and result
handover.
The later
[Microsoft optional-profile decision](../architecture/2026-11-13-microsoft-optional-profile.md)
partially supersedes Microsoft's total provider budget and mandatory Graph
retrieval policy: core work retains its 20-second budget and Graph sources share
a separate 3-second budget under the original caller deadline. It also owns the
sanitized partial-enrichment warning. This note continues to own per-stage
correlation, HTTP observations, credential exclusion, and bounded file logging.

The command optionally appends the same JSON sent to stderr to
`UNIAUTH_LOG_FILE`. Compose enables `/data/uniauth.log` in each environment's
existing persistent data volume when its `.env` omits the variable. Explicitly
empty configuration selects stderr only. The parent directory must exist and
new files use `0600`. One process owns each path. The file opens after listener
reservation and before stateful service initialization, preserving rejected-bind
behavior. Startup errors are returned. Runtime output errors cancel serving and
remain available after graceful cleanup.

Rotation is synchronous before a record would exceed 10 MiB, retaining three
numbered backups with `.1` newest and `.3` oldest. Rename, removal, write, and
close failures propagate. A small command-owned file writer uses standard Go
filesystem operations without introducing another logging backend or dependency.
The [configuration reference](../../../../docs/reference/configuration.md#logs-and-readiness)
owns field definitions; the
[deployment reference](../../../../docs/reference/deployment.md#diagnostic-log-files)
owns path configuration and collection.

## Alternatives

**Unauthenticated probes and aggregate callback duration** establish basic
connectivity and total callback time but omit authenticated upstream processing
and the budget consumed before profile retrieval. They remain useful checks,
but real-flow observations supply the missing attribution.

**Raw upstream errors or HTTP payload logging** can reveal an authorization code,
token, identity data, or sensitive query parameters. Allowlisted error metadata
and transport timings preserve diagnostic value without recording those values.

**Stderr alone** works with a separately configured collector but does not
provide the persistent application-owned file needed for deployment diagnostics.
File output remains optional outside the Compose deployment.

**An unbounded append-only file** preserves all history at an unbounded disk
cost. A fixed active-file size and three backups establish a predictable limit.

**Lumberjack v2.2.1** (`gopkg.in/natefinch/lumberjack.v2`) reduces local
filesystem code, but its
[background retention worker](https://github.com/natefinch/lumberjack/blob/4cb27fcfbb0f35cb48c542c5ea80b7c1d18933d0/lumberjack.go#L376-L383)
discards cleanup errors. Synchronous numbered rotation keeps failures observable
and collection paths predictable without a new dependency.

## Consequences

Operators can distinguish pre-profile budget exhaustion, connection setup,
response-header waiting, and body waiting in real authenticated callbacks.
Successful and failed stages share the same bounded diagnostic structure.
Logging adds a small number of records and synchronous file writes per observed
stage. Each configured path retains up to 40 MiB across the active file and
three backups; older history is discarded through rotation.

Identity data, credentials, request and response bodies, and raw upstream errors
remain excluded. The diagnostics provide evidence for a later behavior change;
they do not establish that increasing a deadline or changing HTTP protocol would
correct a particular provider failure. Live provider verification remains a
post-deployment activity with actual credentials.
