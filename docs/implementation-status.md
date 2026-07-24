# Implementation status

Incremental delivery checkpoints and the wireframe scope boundary are tracked in [`implementation-plan.md`](implementation-plan.md).

| Specification | State | Notes |
|---|---|---|
| 00 System context | In progress | Repository boundaries, module, config, HTTP foundation and local workflow exist. |
| 01 Data models | Implemented source | Migrations, queries, sqlc config and transaction helper are present; integration execution remains. |
| 02 Routing and middleware | Implemented foundation | Full route table is registered; business handlers remain deliberately unavailable rather than returning fake success. |
| 03 Dashboards | Partial | Typed role shells, competitor metrics/training/groups, leisure news/groups, guardian dependant list/form, deduplicated guardian calendar links, and query deadlines exist; calendar enhancement and administrator content remain. |
| 04 Repair workflow | Partial | Object-store adapter and hostile-image validation exist; form and idempotent handler remain. |
| 05 Authentication and consent | Partial | Login, adult registration, guardian dependant creation with transactional responsibility consent, session-backed current-user loading, role guards, logout, and administrator CLI exist; legal-document links and integration execution remain. |
| 06 Accessibility/localisation | Partial | pt-PT locale helpers and HTTP error text exist; full UI and Playwright checks remain. |
| 07 AWS deployment | Not started | Runtime architecture remains defined in the specification bundle. |
| 08 GitHub Actions | Not started | Will follow after application and Terraform verification commands exist. |
| 09 Local development | Implemented source | Compose, env template, Air config and Make targets are present. |
| 10 Acceptance matrix | In progress | Unit-level checks added; integration/e2e/infra evidence remains. |

## Next recommended implementation slice

Implement repair reporting end-to-end, including multipart image validation, idempotency, private object storage, database insertion, and normal/HTMX responses. After that, implement administrator fleet and maintenance scheduling.
