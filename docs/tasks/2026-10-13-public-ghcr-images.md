# Public GHCR Image Validation

Date: 2026-10-13

## Delivered behavior

Release publishes to `ghcr.io/codetreker/uniauth` using `GITHUB_TOKEN` and verifies
anonymous pull before deploying the same digest to staging and prod. The VM needs
no registry login. Deployment continues to copy Compose and run `up -d`.

## Observed validation

| Check | Result |
| --- | --- |
| `make setup` and `make check` | Passed dependency setup, Console build/types/formatting, Go formatting/vet, active links, skill metadata, note lifecycle, and discovery symlinks. |
| actionlint 1.7.12 and diff whitespace | Passed. |
| Independent review | No confirmed critical or major issue. |
| Anonymous GHCR access probe | A pull-token request for `codetreker/uniauth` returned HTTP 403 on 2026-10-13. This cannot distinguish a nonexistent package from a private one. |

Package metadata could not be read because the local GitHub token lacks
`read:packages` scope.

## Package visibility evidence

Official GHCR and access-control documentation confirms that first publication is
private and repository access inheritance excludes visibility. The documented
[Packages REST API](https://docs.github.com/en/rest/packages/packages) and the live
GraphQL mutation schema expose no visibility setter. The
[deployment reference](../reference/deployment.md) links the one-time Public
setting and its sources.

## Verification limits

No GHCR push, package visibility change, or VM deployment was performed.
Publication and package Public setup require the merged workflow and the package
administrator. The build job's anonymous pull checks the actual published digest.
