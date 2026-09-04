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
focus, success/error announcements, interactive registration,
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
- Restored the native closed-`details` contract after the shared disclosure
  styling overrode it. Closed disclosure bodies now use `display: none`, so
  VoiceOver cannot reach form fields or select options before the disclosure is
  expanded.

## Manual evidence

| Check | Environment | Evidence | Result |
| --- | --- | --- | --- |
| Accessibility-tree structure, administrator Today | Codex in-app Chromium browser, macOS | Skip link, complementary account/navigation landmark, named primary and breadcrumb navigation, single main/H1, textual status cues and contentinfo exposed in reading order | Pass |
| Member, tutor and administrator screen-reader journeys | VoiceOver + Chromium (Codex in-app browser), macOS 15.7.7 (24G720), 2026-08-02, Codex with user-authorized local VoiceOver | VoiceOver was active while the browser accessibility tree and VO navigation order were inspected. Member Today, denied 403 and missing 404; tutor Today, dependants and expanded form semantics; administrator Today, members, member detail, news and fleet all retained identity, cumulative capabilities, named landmarks/navigation, one H1, coherent headings, current-location state and resolved ARIA references. Spoken audio was not transcribed. | Pass after closed-disclosure remediation |
| Keyboard-only key journeys | Playwright Chromium plus VoiceOver browser smoke | Registration order, disclosure/form exposure, validation-summary focus and HTMX status focus; the VoiceOver pass confirmed that closed disclosures no longer expose inactive controls and expanded forms expose their names/help in order | Pass |
| Forced colours and reduced motion | Playwright Chromium emulation | Every maintained route preserves a visible focus outline; both media preferences activate | Automated pass |
| River Clubhouse 2.0 structured-planner and photo-removal companion | Product-authority acceptance, 2026-08-20 | The product authority accepted the maintained manual companion for the administrator structured planner (including task return) and profile-photo-removal confirmation at compact widths, and directed delivery to proceed without a new screen-reader session. Browser, operating system and assistive-technology observations were not captured. | Accepted assumption; not independently observed evidence |

No limitation is accepted by this audit without a rationale and a focused
follow-up issue. The explicitly labelled product-authority acceptance above is
limited to this non-release delivery handoff and is not a substitute for an
observed release accessibility audit.

## Commands

- `CI=true make ui-review-screenshots`
- `CI=true make test-e2e`
- `go test ./...`
- `make verify-foundation`
