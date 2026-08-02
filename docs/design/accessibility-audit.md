# WCAG 2.2 AA audit

Issue #61 is the final systematic accessibility audit for Epic #53. It uses
the deterministic six-persona dataset introduced by #62 and the route matrix
maintained by #60.

## Automated contract

Every representative route/persona state is checked in desktop and mobile
contexts for:

- no serious or critical `@axe-core/playwright` violations;
- a non-empty document title, `pt-PT` document language, one visible `h1`, one
  `main` landmark and no skipped heading levels;
- resolved `aria-labelledby` and `aria-describedby` references;
- the skip link as the first keyboard stop and a focus outline at least two CSS
  pixels thick;
- preserved focus indication in forced-colour mode;
- active reduced-motion preferences;
- the responsive, text-resize, 200%/400% zoom, compact-height and 44 CSS-pixel
  touch-target contract documented for #60.

The interaction suite additionally covers keyboard operation, validation
focus, success/error announcements, JavaScript-disabled registration,
authentication, dependent management and repair reporting.

The final clean automated matrix completed with all six persona projects
passing across the desktop/mobile route set.

## Remediation

- `FieldErrorMessage` now always renders its stable error-description target.
  Empty targets are hidden until an error exists, so valid controls no longer
  reference missing DOM IDs and invalid controls retain the same programmatic
  description relationship.
- Removed broken `aria-labelledby` references from the tutor dependent list
  and shared calendar section. Their visible headings retain the native section
  outline without creating an invalid named-region relationship.

## Manual evidence

| Check | Environment | Evidence | Result |
| --- | --- | --- | --- |
| Accessibility-tree structure, administrator Today | Codex in-app Chromium browser, macOS | Skip link, complementary account/navigation landmark, named primary and breadcrumb navigation, single main/H1, textual status cues and contentinfo exposed in reading order | Pass |
| Member, tutor and administrator screen-reader journeys | Named pairing required before #65 | Follow `docs/accessibility-manual-check.md`; record browser, screen reader, OS, date and tester here | Pending |
| Keyboard-only key journeys | Playwright Chromium plus manual checklist | Registration order, Enter activation, disclosure/form operation, validation-summary focus, HTMX status focus and JavaScript-disabled journeys | Automated pass; final manual smoke pending |
| Forced colours and reduced motion | Playwright Chromium emulation | Every maintained route preserves a visible focus outline; both media preferences activate | Automated pass |

No limitation is accepted by this audit without a rationale and a focused
follow-up issue.

## Commands

- `CI=true make ui-review-screenshots`
- `CI=true make test-e2e`
- `go test ./...`
- `make verify-foundation`
