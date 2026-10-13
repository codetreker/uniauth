# Agent Note: Public GHCR Images

Status: implemented

## Problem

Deploying the gateway must not require registry credentials on the VM. Image
publication should use the repository's existing GitHub Actions identity.

## Decision

The release workflow publishes `ghcr.io/codetreker/uniauth` with `GITHUB_TOKEN`
and build-job `packages: write` permission. An OCI source label associates the
package with its repository. A Docker pull using an empty credential configuration
verifies public access through the same short-tag image reference passed to
staging and prod. The [tag-reference decision](2026-10-13-tag-image-references.md)
owns selection of that reference.
The [Compose release decision](2026-10-13-compose-releases.md) owns file transfer
and service startup; this decision replaces its original private-registry login
prerequisite.

## Alternatives

**Private Harbor images** require publishing credentials and VM registry login.
Public GHCR images remove those deployment credentials.

**Repository visibility inheritance** provides package access permissions but does
not make a container package public. Package visibility must be changed explicitly.

## Consequences

An organization package administrator sets the package to Public after its first
publication. Until then, anonymous pull fails and deployment is blocked. Later
versions retain visibility. GitHub has no supported REST or GraphQL operation to
set it from the workflow; [deployment instructions](../../../../docs/reference/deployment.md)
link to the required package setting and official documentation.
