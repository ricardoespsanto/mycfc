# Authenticated system-state migration

This final issue #64 page-family slice migrates the common 403, 404, 500 and
501 responses, plus CSRF rejection, to the approved River Clubhouse system.

## Route and context contract

- Authenticated failures retain the signed-in identity, cumulative capability
  navigation, CSRF-protected logout, and a safe return action to `/today`.
- Anonymous failures use the same typography, page header, module and empty
  state without inventing authenticated context.
- A denied target uses a neutral `/system` shell location. Route-derived
  navigation such as the administration sub-navigation is therefore never
  exposed to a user who cannot access that area.
- Authorization failures explain the missing capability. CSRF failures instead
  explain that the request was rejected and that no change was made.
- Internal and not-implemented states include the middleware request reference
  when available.
- Panic recovery delegates to the same 500 renderer instead of returning its
  former bare HTML fallback.
- Status codes and server-rendered behavior remain unchanged.

The approved #54/#56 route decision remains separate: `/dashboard/coach`
redirects to `/events` in #56, while `/dashboard/moderator` is intentionally
retained until a real moderation destination exists.

## Before and after evidence

The before images render the exact predecessor 403 response markup from Git
history. The after images are captured from deterministic signed-in member
journeys.

| State | Desktop | Mobile |
| --- | --- | --- |
| Before 403 | [desktop](system-state-evidence/before/desktop.png) | [mobile](system-state-evidence/before/mobile.png) |
| After 403 | [desktop](system-state-evidence/after/forbidden-desktop.png) | [mobile](system-state-evidence/after/forbidden-mobile.png) |
| After 404 | [desktop](system-state-evidence/after/not-found-desktop.png) | [mobile](system-state-evidence/after/not-found-mobile.png) |

## Verification

- Unit coverage asserts status, shared page primitives, assets, request
  references, authenticated identity/navigation, safe return actions, and the
  absence of denied administration navigation.
- The focused signed-in member journey asserts real 403/404 response status,
  desktop/mobile presentation, 320 CSS pixel reflow, 200% zoom, keyboard focus,
  and zero serious or critical axe violations.
- `go test ./...` and `make verify-foundation` cover compilation, formatting,
  generated assets and the foundation suite.
- `CI=true make test-e2e` passes all nine application journeys (the six
  UI-review-only screenshot tests are intentionally skipped by this target).
