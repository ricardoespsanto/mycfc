# Tutor and programme page migration evidence

This #64 page-family slice applies the authenticated UI foundation to the tutor and programme workspaces without changing their routes, permissions, database schema, or product behaviour.

## Routes

- `/dashboard/guardian`
- `/dashboard/competition`
- `/dashboard/leisure`
- the shared programme workspace used by the authenticated programme compatibility routes

## Visual evidence

| Workspace | Desktop before | Desktop after | Mobile before | Mobile after |
| --- | --- | --- | --- | --- |
| Tutor | [before](programme-evidence/before/tutor/desktop/dashboard--guardian.png) | [after](programme-evidence/after/tutor/desktop/dashboard--guardian.png) | [before](programme-evidence/before/tutor/mobile/dashboard--guardian.png) | [after](programme-evidence/after/tutor/mobile/dashboard--guardian.png) |
| Competition | [before](programme-evidence/before/athlete/desktop/dashboard--competition.png) | [after](programme-evidence/after/athlete/desktop/dashboard--competition.png) | [before](programme-evidence/before/athlete/mobile/dashboard--competition.png) | [after](programme-evidence/after/athlete/mobile/dashboard--competition.png) |
| Leisure | [before](programme-evidence/before/multi/desktop/dashboard--leisure.png) | [after](programme-evidence/after/multi/desktop/dashboard--leisure.png) | [before](programme-evidence/before/multi/mobile/dashboard--leisure.png) | [after](programme-evidence/after/multi/mobile/dashboard--leisure.png) |

## Verification

- `go test ./...`
- `make verify-foundation`
- authenticated Playwright journeys, including the interactive dependent workflow
- deterministic tutor, athlete and multi-role screenshot journeys with axe, keyboard focus, 320px and simulated 200% zoom checks

The shared repair disclosure was migrated with this slice because it appears directly in every programme workspace. Its server endpoint and form semantics are unchanged.
