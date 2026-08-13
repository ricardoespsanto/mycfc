# Administration operations migration evidence

This #64 page-family slice migrates the news publishing and fleet operations surfaces to the shared authenticated UI without changing their routes, permissions, database schema, or operational semantics.

## Routes

- `/admin/noticias`
- `/admin/fleet`

## Visual evidence

| Route | Desktop before | Desktop after | Mobile before | Mobile after |
| --- | --- | --- | --- | --- |
| News | [before](admin-operations-evidence/before/admin/desktop/admin--noticias.png) | [after](admin-operations-evidence/after/admin/desktop/admin--noticias.png) | [before](admin-operations-evidence/before/admin/mobile/admin--noticias.png) | [after](admin-operations-evidence/after/admin/mobile/admin--noticias.png) |
| Fleet | [before](admin-operations-evidence/before/admin/desktop/admin--fleet.png) | [after](admin-operations-evidence/after/admin/desktop/admin--fleet.png) | [before](admin-operations-evidence/before/admin/mobile/admin--fleet.png) | [after](admin-operations-evidence/after/admin/mobile/admin--fleet.png) |

## Verification

- `go test ./...`
- `make verify-foundation`
- authenticated Playwright journeys for news and maintenance workflows
- deterministic admin screenshot journey with axe, keyboard focus, 320px and simulated 200% zoom checks

News creation and status transitions now use redirect-after-success feedback. Fleet status labels are localized and conveyed by both text and badge cues; the existing HTMX and server form endpoints remain unchanged.
