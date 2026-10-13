# Documentation

| Document | Responsibility |
| --- | --- |
| [Blueprint](blueprint/README.md) | Agreed requirements, architecture, component contracts, and accepted costs. |
| [API reference](reference/api.md) | Implemented product login, identity retrieval, Console, and health APIs. |
| [Configuration reference](reference/configuration.md) | Runtime environment, provider setup, limits, deployment, and validation commands. |
| [Authentication gateway decision](../.agents/notes/implemented/architecture/2026-10-12-authentication-gateway.md) | Ownership, identity handover alternatives, and operating consequences. |
| [Normalized identity decision](../.agents/notes/implemented/architecture/2026-10-13-normalized-identity.md) | Standard profile mapping, complete original data, and wire-format costs. |
| [Authentication gateway receipt](tasks/2026-10-12-authentication-gateway.md) | Observed validation and remaining verification limits for the gateway implementation. |
| [Normalized identity receipt](tasks/2026-10-13-normalized-identity.md) | Observed mapping, handover, coverage, and browser validation for the standard identity contract. |
| [Agent Notes](../.agents/notes/README.md) | Decision-record lifecycle and retention. |

Blueprint requirements explain the intended system. References describe behavior
implemented in source. Task receipts distinguish observed tests from assumptions
and deployment checks. Execution plans live in gitignored `docs/plans/`.
