# Agent Note: Daily Release Image Tags

Status: implemented

## Problem

Image publication, anonymous-access verification, and deployment need a shared
reference so release stages address the image selected for that run. Operators
need each published release to identify its UTC date, position among that day's
published images, and source revision in registry listings and Compose files.

## Decision

The build job allocates a tag in `<yymmdd>-<seq>-<hash-8>` format through
[the release image helper](../../../../scripts/release-image.py), which uses the
Python standard library. It captures the UTC date once when allocation runs,
then obtains all currently published registry tags through authenticated GHCR
queries. HTTP Basic credentials containing the GitHub actor and `GITHUB_TOKEN`
request a repository-scoped `pull,push` bearer token, which authenticates tag
listing. Every `Link` pagination page with `rel=next` is followed before sequence
selection.

Only tags matching the current date and complete requested format participate.
The numeric `seq` is the largest existing matching sequence plus one, or `1`
when none match. `hash-8` is the first eight characters of the source commit
hash. Older `sha-` tags do not participate. An empty tag list, including
`tags: null`, has no published sequence. Only a tag-list response with HTTP
`404` and the registry error `NAME_UNKNOWN` represents an absent repository;
authentication, network, and malformed-response failures abort allocation.

Image publication, anonymous pull verification, and the build job's `image`
output use one full reference containing the allocated tag, such as
`ghcr.io/codetreker/uniauth:261013-3-a1b2c3d4`. Staging and prod consume the output
unchanged when selecting the image in their Compose files. The existing release
concurrency group serializes allocation through publication and deployment;
there is no separate persistent sequence counter.

This decision partially supersedes the digest-promotion choice in the
[Compose release decision](2026-10-13-compose-releases.md). That decision keeps
ownership of manual initiation, the main-ref guard, deployment ordering, and VM
configuration. The [public GHCR decision](2026-10-13-public-ghcr-images.md) owns
image access. The [deployment reference](../../../../docs/reference/deployment.md)
owns the supported operator instructions.

## Alternatives

**Selecting the build digest** was the original release mechanism. It selected
immutable image content across verification and both deployments. The selected
daily tag records the publication sequence and source revision in registry
verification and Compose deployment, and is used throughout the release.

## Consequences

Sequences reflect currently published tags, not queued runs, attempted builds,
or a permanent release history. The date is selected at allocation, so a run
queued before midnight can allocate under the next UTC date. A failure before
publication can leave its sequence available to a later attempt. Once published,
a tag contributes to later allocation even if verification or deployment fails.
Rerunning the full build selects the next published sequence; the same source
commit can therefore produce several release tags. A date without matching tags
starts at `1`. Removing published tags can lower the maximum and permit reuse
of a sequence.

Allocation requires successful authenticated registry access before publication.
The workflow builds once, verifies anonymous access to the allocated reference,
and passes it through staging to prod. Registry tags remain mutable: a tag can
point to different content after publication, and matching references do not
establish identical content across separate pulls. Manual initiation, main-only
execution, staging-before-prod ordering, and public image access retain their
existing contracts. Controlled registry fixtures can verify allocation and
recognized repository absence; they do not establish live first-publication
authentication with a workflow job token. That verification belongs to deployment
validation.
