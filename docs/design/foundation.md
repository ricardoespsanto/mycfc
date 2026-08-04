# River Clubhouse production foundation

The authenticated application foundation lives in `ui/static/src/app.css` and `ui/components/foundation.templ`. It implements the approved direction from #63 without changing the application shell (#55) or Today composition (#57).

## Tokens

- Brand and structure: `--c-brand-*`, `--c-river-*`.
- Surfaces: `--c-page`, `--c-raised`, `--c-inset`, `--c-interactive`.
- Content and state: `--c-text`, `--c-muted`, `--c-border`, `--c-focus`, and success/warning/error pairs.
- Type: `--font-sans`, the `--text-*` scale, tight/body line heights, and `--measure` for readable copy.
- Layout: `--space-1` through `--space-7`, three radii, raised shadow, and the compact breakpoint.

Public-site rules remain scoped under `.public-body`. New authenticated components consume tokens rather than literal page-local colours.

## Primitives

- `PageHeader` and `PageAction` establish page title, introduction, and action order.
- `Surface` provides a river-accented module rather than a generic floating white card.
- `Badge` includes a shape cue and readable label; colour is supplemental.
- `EmptyState` pairs a decorative icon, reason, and next-step copy.
- `Callout` selects status/alert semantics by severity.
- `DataRegion` creates a named, keyboard-focusable responsive table viewport.
- `Icon` requires an accessible label unless explicitly decorative.

The component catalogue demonstrates default, hover/focus-ready, pressed, selected, disabled, expanded, success, warning, error, form-help, invalid, and empty states with Portuguese content.

## Proof pages

- Sparse: `/announcements` uses the shared header, surface, badge, and empty-state primitives.
- Dense: `/admin/membros` uses the shared header/action contract, searchable module, status badges, responsive semantic table, and disclosure form.

| Proof | Before desktop | After desktop | Before mobile | After mobile |
| --- | --- | --- | --- | --- |
| Avisos (sparse) | [capture](foundation-evidence/before/announcements-desktop.jpg) | [capture](foundation-evidence/after/announcements-desktop.png) | [shared sparse-list baseline](foundation-evidence/before/announcements-mobile.png) | [capture](foundation-evidence/after/announcements-mobile.png) |
| Membros (dense) | [capture](foundation-evidence/before/members-desktop.png) | [capture](foundation-evidence/after/members-desktop.png) | [capture](foundation-evidence/before/members-mobile.png) | [capture](foundation-evidence/after/members-mobile.png) |

The prior mobile harness did not retain an Avisos-specific capture, so its before evidence uses the structurally identical sparse list treatment from Eventos; the desktop before capture is Avisos itself. The table intentionally scrolls at narrow widths so column relationships and header semantics are preserved at 320 CSS px and browser zoom. #64 can migrate remaining pages incrementally using these same primitives.
