# Implementation status

Incremental delivery checkpoints and the wireframe scope boundary are tracked in [`implementation-plan.md`](implementation-plan.md).

| Specification | State | Notes |
|---|---|---|
| 00 System context | In progress | Repository boundaries, module, config, HTTP foundation and local workflow exist. |
| 01 Data models | Implemented source | Migrations, queries, sqlc config and transaction helper are present; integration execution remains. |
| 02 Routing and middleware | Implemented foundation | Full route table is registered; business handlers remain deliberately unavailable rather than returning fake success. |
| 03 Dashboards | Partial | Typed role shells, competitor metrics/training/groups, leisure news/groups, public-calendar links, and query deadlines exist; calendar enhancement, guardian/admin content, and workflow forms remain. |
| 04 Repair workflow | Partial | Object-store adapter and hostile-image validation exist; form and idempotent handler remain. |
| 05 Authentication and consent | Partial | Login, adult registration, transactional consent creation, session-backed current-user loading, role guards, logout, and administrator CLI exist; dependent flow, legal-document links, and integration execution remain. |
| 06 Accessibility/localisation | Partial | pt-PT locale helpers and HTTP error text exist; full UI and Playwright checks remain. |
| 07 AWS deployment | Not started | Runtime architecture remains defined in the specification bundle. |
| 08 GitHub Actions | Not started | Will follow after application and Terraform verification commands exist. |
| 09 Local development | Implemented source | Compose, env template, Air config and Make targets are present. |
| 10 Acceptance matrix | In progress | Unit-level checks added; integration/e2e/infra evidence remains. |

## Next recommended implementation slice

Implement authentication and adult registration end-to-end because it establishes session-backed current-user loading, templ form conventions, transaction boundaries and the first complete browser flow. After that, implement guardian dependants and the repair workflow.
