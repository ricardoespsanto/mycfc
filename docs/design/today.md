# Today vertical slice

Issue #57 establishes `/today` as the quality bar for the authenticated MyCFC rollout. It uses the River Clubhouse foundation and shell while composing only existing, authorised product data.

## Composition contract

Today renders no more than five modules in this order:

1. the next still-relevant item and up to four events intersecting today;
2. the club distance leaderboard;
3. up to three named dependants when the signed-in adult has any;
4. up to three pending repair items for administrators;
5. up to five canonical shortcuts, including at most one programme workspace.

The same page is composed from cumulative capabilities. There are no exclusive role variants, speculative actions, or duplicated management workflows. The empty agenda explains what happens next and links to its owning destination.
Reader-facing notices no longer form a Today module: the global bell exposes the
unread count and recent notices from every authenticated page.

## Review evidence

The before captures use the original representative page for each context: the old Today card for an ordinary member, and the old tutor, competition, and fleet workspaces for the other contexts. The after captures show the shared Today composition using deterministic review personas.

| Context | Before desktop | After desktop | Before mobile | After mobile |
| --- | --- | --- | --- | --- |
| Ordinary member | [capture](today-evidence/before/member-desktop.png) | [capture](today-evidence/after/member-desktop.png) | [capture](today-evidence/before/member-mobile.png) | [capture](today-evidence/after/member-mobile.png) |
| Tutor | [capture](today-evidence/before/tutor-desktop.png) | [capture](today-evidence/after/tutor-desktop.png) | [capture](today-evidence/before/tutor-mobile.png) | [capture](today-evidence/after/tutor-mobile.png) |
| Athlete | [capture](today-evidence/before/athlete-desktop.png) | [capture](today-evidence/after/athlete-desktop.png) | [capture](today-evidence/before/athlete-mobile.png) | [capture](today-evidence/after/athlete-mobile.png) |
| Administrator | [capture](today-evidence/before/admin-desktop.png) | [capture](today-evidence/after/admin-desktop.png) | [capture](today-evidence/before/admin-mobile.png) | [capture](today-evidence/after/admin-mobile.png) |

## Verification

- Handler tests cover event-day bounds, personalized content, query limits, dependants, programme shortcuts, administrator operations, and user-facing status labels.
- The UI review harness asserts the shared modules plus tutor, athlete, multi-capability, and administrator variants before capturing all six personas at 1440×900 and 375×812.
- Each Today capture is also checked at 320 CSS px for horizontal document overflow.
- Axe reports no serious or critical violations in the persona matrix.
- The CI browser suite retains keyboard, 200% zoom, no-JavaScript, registration, tutor, athlete, and administrator journeys.
