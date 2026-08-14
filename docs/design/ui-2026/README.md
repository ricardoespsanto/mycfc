# MyCFC UI 2026 — River Clubhouse 2.0

This design packet supports the **River Clubhouse 2.0** GitHub epic and its child issues. It evolves the completed authenticated UI makeover (#53) into a coherent task-oriented interaction system for the larger application that now exists.

## Product direction

- Keep the approved River Clubhouse identity and cumulative-capability application shell.
- Treat JavaScript as an expected application dependency.
- Keep Go handlers, server authorization, CSRF, validation, audit and persisted state authoritative.
- Use task complexity and consequence—not the former no-JavaScript rule—to select an interaction.
- Preserve recoverability through stable task URLs and server-rendered validation responses where useful.
- Deliver one independently reviewable page family at a time.
- Do not introduce a SPA framework, new business policy, schema migration or feature flag solely for this initiative.

## Interaction model

| Task | Surface |
|---|---|
| Immediate, low-risk, one or two fields | Inline control |
| Short, bounded, contextual task | Centred modal |
| Medium contextual task | Modal side sheet |
| Long, conditional, sensitive, file-based or comparison-dependent task | Dedicated task route |
| Irreversible or destructive action | Confirmation dialog backed by a guarded task route |
| Supporting detail | Disclosure |
| Reference content that does not block the current task | Non-modal drawer |

Mobile adapts modal and side-sheet tasks into full-height task surfaces with a sticky heading and action footer.

## Page archetypes

1. Overview/home.
2. Collection or work queue.
3. Record detail.
4. Builder/planner.
5. Account/settings.
6. Authentication and system state.

## Wireframes

- [Interaction decision matrix](interaction-surfaces.svg)
- [Collection with modal side-sheet task](collection-task.svg)
- [Structured-training planner](structured-training-planner.svg)
- [Mobile full-height task](mobile-task.svg)

The wireframes use representative pt-PT content and annotate interaction intent. They are not pixel specifications.

## Cross-cutting acceptance contract

- One visually primary top-level action per page.
- Invalid submissions return to the owning task, preserve safe values and focus an actionable error summary.
- Successful mutations retain the active subject, tab/filter/page and originating object.
- Focus is contained inside modal surfaces and returned to the exact opener on close.
- Long Portuguese names and labels remain meaningful at 320 CSS px and high zoom.
- True comparison tables retain headers in a named horizontal region on mobile.
- Status is conveyed with text and structure, not colour alone.
- Existing server authorization and business behaviour do not move into JavaScript.
- Representative keyboard, screen-reader, forced-colour, reduced-motion, compact-height, zoom/reflow, axe and JavaScript-asset-failure evidence is required.

## Current evidence

The audit found mixed creation drawers, true dialogs, dedicated pages and inline forms without a predictable selection rule. Structured training contains the strongest modern interactions but also the most serious recovery gaps: several nested invalid submissions leave the workspace, discard entered values and present a generic rejected-request state.

A current-branch UI-review run on 2026-08-14 produced one passing test and eight failures. Findings included a narrow registration checkbox target, Fleet tab-state expectation drift and duplicate “Avisos” accessible names for role-heavy personas. Because the checkout contained concurrent structured-training work, this run is planning evidence rather than a clean-main release result.
