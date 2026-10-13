# Documentation

| Document | Responsibility |
| --- | --- |
| [Blueprint](blueprint/README.md) | Agreed requirements, architecture, component contracts, and accepted costs. |
| [API reference](reference/api.md) | Implemented product login, identity retrieval, Console, and health APIs. |
| [Configuration reference](reference/configuration.md) | Runtime environment, provider setup, limits, deployment, and validation commands. |
| [Deployment reference](reference/deployment.md) | Docker Compose releases, GitHub settings, and VM prerequisites. |
| [Compose release decision](../.agents/notes/implemented/process/2026-10-13-compose-releases.md) | Image promotion and deployment ownership. |
| [Public GHCR decision](../.agents/notes/implemented/process/2026-10-13-public-ghcr-images.md) | Public image access and package visibility ownership. |
| [GHCR validation](tasks/2026-10-13-public-ghcr-images.md) | Publication research and external setup limits. |
| [Release validation](tasks/2026-10-13-compose-releases.md) | Observed release checks and verification limits. |
| [Authentication gateway decision](../.agents/notes/implemented/architecture/2026-10-12-authentication-gateway.md) | Ownership, identity handover alternatives, and operating consequences. |
| [Implementation receipt](tasks/2026-10-12-authentication-gateway.md) | Observed validation and remaining verification limits for the implementation task. |
| [Agent Notes](../.agents/notes/README.md) | Decision-record lifecycle and retention. |

Blueprint requirements explain the intended system. References describe behavior
implemented in source. Task receipts distinguish observed tests from assumptions
and deployment checks. Execution plans live in gitignored `docs/plans/`.
