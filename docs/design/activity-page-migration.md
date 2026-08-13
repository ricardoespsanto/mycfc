# Activity page migration evidence

This is the first coherent page-family slice for #64. It applies the authenticated UI foundation and interaction contract to the activity routes without changing their permissions, database schema, or business behaviour.

## Routes

- `/events` and `/events/{id}`
- `/treinos`
- `/announcements` and `/announcements/{id}`

The full authenticated-route inventory and remaining owners are tracked in [authenticated-route-migration.md](authenticated-route-migration.md).

## Visual evidence

| Route | Desktop before | Desktop after | Mobile before | Mobile after |
| --- | --- | --- | --- | --- |
| Events | [before](activity-evidence/before/member/desktop/events.png) | [after](activity-evidence/after/member/desktop/events.png) | [before](activity-evidence/before/member/mobile/events.png) | [after](activity-evidence/after/member/mobile/events.png) |
| Announcements | [before](activity-evidence/before/member/desktop/announcements.png) | [after](activity-evidence/after/member/desktop/announcements.png) | [before](activity-evidence/before/member/mobile/announcements.png) | [after](activity-evidence/after/member/mobile/announcements.png) |
| Training | [before](activity-evidence/before/coach/desktop/treinos.png) | [after](activity-evidence/after/coach/desktop/treinos.png) | [before](activity-evidence/before/coach/mobile/treinos.png) | [after](activity-evidence/after/coach/mobile/treinos.png) |

## Verification

- `go test ./...`
- `make verify-foundation`
- `CI=true make test-e2e`
- `CI=true make ui-review-screenshots`

The screenshot gate runs axe checks and checks the activity routes at 320px, simulated 200% zoom, and with keyboard focus. The existing event and announcement journeys exercise their server-authoritative workflows; the wider suite covers interactive form validation and recovery.
