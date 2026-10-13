# Documentation

| Document | Responsibility |
| --- | --- |
| [Blueprint](blueprint/README.md) | Agreed requirements, architecture, component contracts, and accepted costs. |
| [API reference](reference/api.md) | Implemented product login, identity retrieval, Console, and health APIs. |
| [Configuration reference](reference/configuration.md) | Runtime environment, provider setup, limits, deployment, and validation commands. |
| [SDK reference](reference/sdks.md) | Go and JavaScript product-backend clients, callback state responsibilities, raw identity data, and local package use. |
| [Authentication gateway decision](../.agents/notes/implemented/architecture/2026-10-12-authentication-gateway.md) | Ownership, identity handover alternatives, and operating consequences. |
| [Product SDK decision](../.agents/notes/implemented/feature/2026-10-13-product-sdks.md) | SDK responsibilities, transport and data contracts, and package-consumer validation. |
| [Implementation receipt](tasks/2026-10-12-authentication-gateway.md) | Observed validation and remaining verification limits for the initial implementation. |
| [Console correction receipt](tasks/2026-10-12-console-review-fixes.md) | Origin serialization and same-App mutation validation. |
| [Product SDK receipt](tasks/2026-10-13-product-sdks.md) | Observed package and protocol validation for the SDK delivery. |
| [Agent Notes](../.agents/notes/README.md) | Decision-record lifecycle and retention. |

Blueprint requirements explain the intended system. References describe behavior
implemented in source. Task receipts distinguish observed tests from assumptions
and deployment checks. Execution plans live in gitignored `docs/plans/`.
