# UI makeover release gate

This is the completion matrix for epic #53. It ties the maintained capability
inventory in `ui-review-harness.md` to deterministic automated evidence and
the completed named-screen-reader check.

## Automated route and journey matrix

| Persona | Representative routes and journey | Automated contract |
| --- | --- | --- |
| Member | `/today`, `/events`, `/announcements`, denied `/admin/fleet`, `/missing` | shared shell and identity, sparse/empty states, 403/404 orientation, responsive, keyboard, axe |
| Tutor | `/today`, `/dashboard/guardian` | dependant context, direct deep link, validation and interactive dependant creation |
| Athlete | `/today`, `/dashboard/competition`, `/treinos` | programme capability/navigation, direct deep links, training state, administrator-granted membership journey |
| Coach | `/today`, `/events`, `/treinos` | coach capability as context rather than a false destination, staff authoring surfaces |
| Administrator | `/today`, `/admin/membros`, member detail, `/admin/noticias`, `/admin/fleet` | cumulative navigation, breadcrumbs, validation/focus recovery, status transitions, maintenance and interactive account workflow |
| Multi-capability | `/today`, `/dashboard/leisure`, `/dashboard/competition`, `/events` | additive programme/staff context, stable navigation ordering and no role switcher |

`CI=true make ui-review-screenshots` executes every row at desktop and mobile
sizes. Each route checks a unique non-empty title, Portuguese document language,
one visible `h1`, one `main`, heading order, resolved ARIA references, skip-link
and focus visibility, forced colours, reduced motion, 320/375/768 CSS-pixel
overflow, the effective 200% zoom width, 200% text enlargement, expanded
disclosures, frequent 44px touch targets, and serious/critical axe violations.
Failures name the persona in the test title and include the route and failed
contract in the assertion message.

## Supporting automated evidence

| Contract | Evidence |
| --- | --- |
| Navigation construction/order, cumulative capabilities, nested current route | `internal/handlers/dashboard_test.go`, `ui/components/components_test.go` |
| Identity, capability and active-subject context | dashboard handler/component tests plus the six-persona UI matrix |
| Breadcrumbs, local navigation, page headers and actions | component tests and member/event detail browser journeys |
| Validation, empty and status states | component/handler tests and guardian, event, fleet, news and announcement journeys |
| Canonical and compatibility routes | `internal/app/router_test.go` and the `/dashboard/member?from=legacy` browser assertion; capability middleware remains outside each redirect handler |
| Interactive resilience | public navigation, authenticated member/tutor navigation, dependant and repair workflows, and administrator member deactivation in `e2e/auth.spec.mjs` |
| Generated source/assets | CI `generated-and-format`: regenerate, format-check and require a clean diff |

The application gate is:

```bash
make verify-foundation
CI=true make test-e2e
CI=true make ui-review-screenshots
```

Screenshots under `artifacts/ui-review/` are review evidence, not pixel-diff
tests. Curated before/after evidence is retained under `docs/design/*-evidence/`.

## Manual evidence

| Check | State | Evidence or follow-up |
| --- | --- | --- |
| Desktop/mobile visual pass across all six personas | Complete | Approved staged visual passes and curated evidence for #57, #58, #60 and #64 |
| Keyboard-only shell, forms, disclosures and focus recovery | Complete | Automated traversal plus manual browser inspection recorded in `accessibility-audit.md` |
| macOS VoiceOver named-screen-reader pass | Complete | VoiceOver + Chromium (Codex in-app browser) on macOS 15.7.7 (24G720), 2026-08-02. Member, tutor, administrator, 403 and 404 journeys are recorded in `accessibility-audit.md`; the pass found and verified the closed-disclosure accessibility-tree remediation. |

The automated, visual, keyboard and named-screen-reader evidence required for
Epic #53 is complete.
