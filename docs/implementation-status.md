# Implementation status

Incremental delivery checkpoints and the wireframe scope boundary are tracked in [`implementation-plan.md`](implementation-plan.md).

| Specification | State | Notes |
|---|---|---|
| 00 System context | In progress | Repository boundaries, module, config, HTTP foundation and local workflow exist. |
| 01 Data models | Implemented source | Migrations, queries, sqlc config and transaction helper are present; maintenance scheduling has PostgreSQL integration coverage. |
| 02 Routing and middleware | Implemented foundation | Full route table is registered; business handlers remain deliberately unavailable rather than returning fake success. |
| 03 Dashboards | Partial | Typed role shells, competitor metrics/training/groups, leisure news/groups, guardian dependant list/form, administrator fleet/repair/maintenance visibility, and browser-fetched public calendars with accessible no-JavaScript fallbacks exist; initial Playwright coverage checks competitor calendar fallback, guardian dependant creation, and responsive overflow. |
| 04 Repair workflow | Partial | Repair reporting with multipart validation, idempotency, private object storage, and administrator visibility exists; PostgreSQL concurrent idempotency and MinIO encrypted upload/delete integration coverage plus no-JavaScript browser photo upload and same-key retry evidence exist. |
| 05 Authentication and consent | Partial | Login, adult registration, guardian dependant creation with transactional responsibility consent, configuration-backed legal-document links, session-backed current-user loading, role guards, logout, and administrator CLI exist; integration execution remains. |
| 06 Accessibility/localisation | Partial | pt-PT locale helpers and HTTP error text exist; initial Playwright/axe coverage covers registration plus competitor and guardian dashboards, while full page-state accessibility checks remain. |
| 07 AWS deployment | Implemented source | Terraform bootstrap and production foundations cover state, private network, RDS, S3, ECR, ALB/WAF, ECS, and GitHub OIDC; operator values and live AWS plan/apply evidence remain. |
| 08 GitHub Actions | Implemented source | Pinned CI, infrastructure, deployment, and Dependabot workflow foundations exist; protected environment configuration and live execution remain. |
| 09 Local development | Implemented source | Compose, env template, Air config and Make targets are present. |
| 10 Acceptance matrix | In progress | Unit-level checks added; integration/e2e/infra evidence remains. |

## Next recommended implementation slice

Complete acceptance evidence: PostgreSQL/MinIO integration coverage, browser accessibility and no-JavaScript flows, then production infrastructure and CI.
