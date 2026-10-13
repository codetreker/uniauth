# Agent Note: Short-Tag Release Image References

Status: implemented

## Problem

Image publication, anonymous-access verification, and deployment need a shared
reference so release stages address the image selected for that run. Operators
also need a reference that identifies the source revision in registry listings
and deployed Compose files.

## Decision

The build job derives the full image reference once as
`ghcr.io/${GITHUB_REPOSITORY}:sha-${GITHUB_SHA:0:16}`. The tag contains `sha-`
followed by the first 16 characters of the source commit hash. Image publication,
anonymous pull verification, and the build job's `image` output use that same
reference. Staging and prod consume the output unchanged when selecting the
image in their Compose files.

This decision partially supersedes the digest-promotion choice in the
[Compose release decision](2026-10-13-compose-releases.md). That decision keeps
ownership of manual initiation, the main-ref guard, deployment ordering, and VM
configuration. The [public GHCR decision](2026-10-13-public-ghcr-images.md) owns
image access. The [deployment reference](../../../../docs/reference/deployment.md)
owns the supported operator instructions.

## Alternatives

**Selecting the build digest** was the original release mechanism. It selected
immutable image content across verification and both deployments. The selected
short-tag reference carries the source revision through registry verification
and Compose deployment, and is used consistently throughout the release.

## Consequences

The reference checked by anonymous pull is also the reference inserted into
both deployment files. The workflow builds once and passes one tag reference
through staging to prod.

Registry tags are mutable. Publishing the same source commit again reuses its
tag, and a tag can point to different content after publication. Matching tag
strings therefore do not establish identical image content across separate
pulls. Manual initiation, main-only execution, staging-before-prod ordering, and
public image access retain their existing contracts.
