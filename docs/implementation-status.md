# Implementation status

This document records the current implementation boundary. Planned and deferred work is tracked in the [MyCFC Delivery Project](https://github.com/users/ricardoespsanto/projects/1).

| Specification | State | Notes |
|---|---|---|
| 00 System context | In progress | Repository boundaries, module, config, HTTP foundation and local workflow exist; a public CFC landing page with locally embedded imagery, contacts, account calls to action, and club-site links is available at `/`. |
| 01 Data models | Implemented source | Migrations, queries, sqlc config and transaction helper are present; maintenance scheduling has PostgreSQL integration coverage. |
| 02 Routing and middleware | Implemented foundation | Full route table is registered; business handlers remain deliberately unavailable rather than returning fake success. |
| 03 Dashboards | Partial | Typed role shells, competitor metrics/training/groups, leisure news/groups, guardian dependant list/form, administrator fleet/repair/maintenance visibility, and browser-fetched public calendars with accessible no-JavaScript fallbacks exist; Playwright covers competitor, leisure, guardian, and administrator role flows. |
| 04 Repair workflow | Partial | Repair reporting with multipart validation, idempotency, private object storage, and administrator visibility exists; PostgreSQL concurrent idempotency and MinIO encrypted upload/delete integration coverage plus no-JavaScript browser photo upload and same-key retry evidence exist. |
| 05 Authentication and consent | Partial | Login, adult registration, guardian dependant creation with transactional responsibility consent, configuration-backed legal-document links, session-backed current-user loading, role guards, logout, and administrator CLI exist; PostgreSQL integration coverage verifies consent persistence, registration rollback, guardian ownership, and dependant limits. |
| 06 Accessibility/localisation | Partial | pt-PT locale helpers and HTTP error text exist; Playwright covers registration keyboard navigation, keyboard logout, and responsive competitor/guardian flows, while full page-state accessibility checks remain. |
| 07 AWS deployment | Implemented source | Terraform bootstrap and production foundations cover state, private network, RDS, S3, ECR, ALB/WAF, ECS, and GitHub OIDC; operator values and live AWS plan/apply evidence remain. |
| 08 GitHub Actions | Implemented source | Pinned CI, infrastructure, deployment, and Dependabot workflow foundations exist; protected environment configuration and live execution remain. |
| 09 Local development | Implemented source | Compose, env template, Air config and Make targets are present. |
| 10 Acceptance matrix | In progress | Unit-level checks added; integration/e2e/infra evidence remains. |
