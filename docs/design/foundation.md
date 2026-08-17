# River Clubhouse production foundation

The authenticated application foundation lives in `ui/static/src/app.css` and `ui/components/foundation.templ`. It implements the approved direction from #63 without changing the application shell (#55) or Today composition (#57).

## Tokens

- Brand and structure: `--c-brand-*` and the complete consumed `--c-river-*` scale (`100`, `200`, `600`, `700`, `800`, `900`).
- Surfaces: `--c-page`, `--c-raised`, `--c-inset`, `--c-interactive` and the `--c-surface-*` semantic aliases. The compatibility names `--c-surface` and `--c-text-muted` resolve to the same system; they are not a parallel palette.
- Content and state: `--c-text`, `--c-muted`, `--c-border`, `--c-border-strong`, `--c-focus`, and success/warning/error pairs.
- Type: `--font-sans`, the `--text-*` scale, tight/body line heights, and `--measure` for readable copy.
- Layout: `--space-1` through `--space-7`, radii, control/page dimensions, and low/raised/overlay shadows.
- Motion: fast/base durations and the standard easing curve. Motion only clarifies state changes; reduced-motion preference reduces every duration to an effectively immediate change.

Public-site rules remain scoped under `.public-body`. New authenticated components consume tokens rather than literal page-local colours.

## Primitives

- `PageHeader` and `PageAction` establish page title, introduction, and action order.
- `PageHeader` renders at most one primary action. A second requested primary action and an unknown variant are safely presented as secondary.
- `Surface` provides a river-accented module rather than a generic floating white card.
- `Badge` includes a shape cue and readable label; colour is supplemental.
- `EmptyState` pairs a decorative icon, reason, and next-step copy.
- `Callout` selects status/alert semantics by severity.
- `DataRegion` creates a named, keyboard-focusable responsive table viewport.
- `FormSection` and `FieldGrid` group related fields without imposing feature-specific layout, and collapse naturally when labels or values need more room.
- `StatusSummary` provides a semantic definition list for compact record state; it is not a dashboard-card substitute.
- `Icon` requires an accessible label unless explicitly decorative.

The component catalogue demonstrates default, hover/focus-ready, pressed, selected, disabled, destructive, expanded, success, warning, error, form-help, invalid, and empty states with Portuguese content. Its shared styles include explicit forced-colour and reduced-motion treatments. Long action, badge, heading, field and status labels wrap instead of being truncated.

## Action hierarchy

- **Primary**: the single most important next step for the current page or bounded task. Use `action--primary` explicitly; element type alone never promotes an action.
- **Secondary**: a useful alternative that does not complete the main task. Unclassified authenticated buttons use this neutral treatment for migration safety.
- **Quiet**: low-emphasis navigation or contextual controls such as **Cancelar**, **Fechar** and **Mais ações**.
- **Destructive**: an action with a harmful or difficult-to-reverse result. Its label names the object and effect; hover and forced-colour states remain distinct.

Within `FormActions`, or as a direct child of the shared `form-layout` bounded-task contract, an unclassified submit button is the task's primary completion action. Elsewhere, a mutation must opt into primary styling. Links navigate and buttons mutate; visual hierarchy does not change server authority or semantics.

## Collections: comparison table or entity rows

Choose from the relationship users need to understand:

| Use a comparison table when… | Use responsive entity rows when… |
| --- | --- |
| users compare the same attributes across several records; | users scan each record as a self-contained subject; |
| column headers carry meaning that must remain associated with every value; | metadata can follow a clear label/value or title/summary order; |
| sorting or cross-row numeric comparison is central to the task. | the primary action is opening or acting on one record. |

A true comparison table keeps semantic table markup and its headers inside a named `DataRegion`; narrow screens scroll horizontally rather than discarding relationships. Entity rows use `RecordList`/`RecordItem`, allow text to wrap, and move contextual actions below the record at compact widths. A page must not switch a real comparison table into unlabeled card fragments solely to avoid horizontal scrolling.

## CSS organization

`ui/static/src/app.css` is ordered as: compatibility baseline, River Clubhouse tokens, authenticated primitives, page patterns/feature exceptions, application shells, and the isolated public website. Existing compatibility rules remain until repository search and migrated-consumer evidence make their removal safe.

## Proof pages

- Sparse: `/announcements` uses the shared header, surface, badge, and empty-state primitives.
- Dense: `/admin/membros` uses the shared header/action contract, searchable module, status badges, responsive semantic table, and disclosure form.

| Proof | Before desktop | After desktop | Before mobile | After mobile |
| --- | --- | --- | --- | --- |
| Avisos (sparse) | [capture](foundation-evidence/before/announcements-desktop.jpg) | [capture](foundation-evidence/after/announcements-desktop.png) | [shared sparse-list baseline](foundation-evidence/before/announcements-mobile.png) | [capture](foundation-evidence/after/announcements-mobile.png) |
| Membros (dense) | [capture](foundation-evidence/before/members-desktop.png) | [capture](foundation-evidence/after/members-desktop.png) | [capture](foundation-evidence/before/members-mobile.png) | [capture](foundation-evidence/after/members-mobile.png) |

The prior mobile harness did not retain an Avisos-specific capture, so its before evidence uses the structurally identical sparse list treatment from Eventos; the desktop before capture is Avisos itself. The table intentionally scrolls at narrow widths so column relationships and header semantics are preserved at 320 CSS px and browser zoom. #64 can migrate remaining pages incrementally using these same primitives.
