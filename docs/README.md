# Documentation

| Document | Responsibility |
| --- | --- |
| [Blueprint](blueprint/README.md) | Agreed requirements, architecture, component contracts, and accepted costs. |
| [API reference](reference/api.md) | Implemented product login, identity retrieval, Console, and health APIs. |
| [Configuration reference](reference/configuration.md) | Runtime environment, provider setup, limits, and validation commands. |
| [Deployment reference](reference/deployment.md) | Docker Compose releases, GitHub settings, VM preparation, and recovery. |
| [Authentication gateway decision](../.agents/notes/implemented/architecture/2026-10-12-authentication-gateway.md) | Ownership, identity handover alternatives, and operating consequences. |
| [Compose release decision](../.agents/notes/implemented/process/2026-10-13-compose-releases.md) | Immutable promotion, environment isolation, and replacement costs. |
| [Implementation receipt](tasks/2026-10-12-authentication-gateway.md) | Observed validation and remaining verification limits for the implementation task. |
| [Release workflow receipt](tasks/2026-10-13-compose-releases.md) | Container, Compose, workflow, and repository validation for release delivery. |
| [Agent Notes](../.agents/notes/README.md) | Decision-record lifecycle and retention. |

Blueprint requirements explain the intended system. References describe behavior
implemented in source. Task receipts distinguish observed tests from assumptions
and deployment checks. Execution plans live in gitignored `docs/plans/`.
