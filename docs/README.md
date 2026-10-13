# Documentation

| Document | Responsibility |
| --- | --- |
| [Blueprint](blueprint/README.md) | Agreed requirements, architecture, component contracts, and accepted costs. |
| [API reference](reference/api.md) | Implemented product login, normalized identity retrieval, Console, and health APIs. |
| [Configuration reference](reference/configuration.md) | Runtime environment, provider setup, limits, deployment, and validation commands. |
| [SDK reference](reference/sdks.md) | Go and JavaScript product clients, normalized nullable identities, state responsibilities, raw JSON, and local package use. |
| [Deployment reference](reference/deployment.md) | Docker Compose releases, GitHub settings, and VM prerequisites. |
| [Authentication gateway decision](../.agents/notes/implemented/architecture/2026-10-12-authentication-gateway.md) | Ownership, identity handover alternatives, and operating consequences. |
| [Normalized identity decision](../.agents/notes/implemented/architecture/2026-10-13-normalized-identity.md) | Standard profile mapping, complete original data, and wire-format costs. |
| [Product SDK decision](../.agents/notes/implemented/feature/2026-10-13-product-sdks.md) | SDK responsibilities, transport and normalized data contracts, and package-consumer validation. |
| [Compose release decision](../.agents/notes/implemented/process/2026-10-13-compose-releases.md) | Manual release initiation and Compose deployment ownership. |
| [Tag-reference decision](../.agents/notes/implemented/process/2026-10-13-tag-image-references.md) | UTC date, published-tag sequence, and commit hash allocation shared by publication, anonymous pull, and deployment. |
| [Public GHCR decision](../.agents/notes/implemented/process/2026-10-13-public-ghcr-images.md) | Public image access and package visibility ownership. |
| [Authentication gateway receipt](tasks/2026-10-12-authentication-gateway.md) | Original gateway implementation checks and verification limits. |
| [Console correction receipt](tasks/2026-10-12-console-review-fixes.md) | Origin serialization and same-App mutation validation. |
| [Normalized identity receipt](tasks/2026-10-13-normalized-identity.md) | Mapping, handover, coverage, and browser validation for the standard identity contract. |
| [Product SDK receipt](tasks/2026-10-13-product-sdks.md) | Original SDK delivery validation before normalized contract integration. |
| [SDK/main integration receipt](tasks/2026-10-13-sdk-main-integration.md) | SDK alignment with normalized identities, deployment merge resolution, and updated validation. |
| [Release validation](tasks/2026-10-13-compose-releases.md) | Observed release checks and verification limits. |
| [GHCR validation](tasks/2026-10-13-public-ghcr-images.md) | Publication research and external setup limits. |
| [Agent Notes](../.agents/notes/README.md) | Decision-record lifecycle and retention. |

Blueprint requirements explain the intended system. References describe behavior
implemented in source. Task receipts distinguish observed tests from assumptions
and deployment checks. Execution plans live in gitignored `docs/plans/`.
