# Accessibility manual check

This checklist is the maintained manual companion to the automated WCAG 2.2
AA contract. Record the browser, assistive technology, operating system, date
and tester in `docs/design/accessibility-audit.md` for every release pass.

## Keyboard and focus

- Complete login, registration, Today navigation, dependent creation, repair
  reporting, event response, training authoring and the administrator member,
  news and fleet workflows without a pointing device.
- Confirm the skip link is the first focusable control and becomes visible on focus.
- Confirm focus order follows the visual and semantic order.
- Confirm every focus indicator remains clearly visible at 200% zoom.
- Confirm HTMX success and validation swaps move focus to the correct heading or error summary.
- Confirm disclosures open and close with Enter and Space, Escape is not needed
  to leave them, and native confirmation controls act on key release.

## Screen reader

- Confirm page titles and the single H1 identify each page.
- Confirm landmarks and navigation labels are useful and non-duplicative.
- Confirm required state, help text and field errors are announced with each control.
- Confirm status messages are announced once and validation errors are urgent without being repeated.
- Confirm repair-image alternative text describes context rather than filenames.
- Confirm member, tutor and administrator journeys in the named
  browser/screen-reader pairing, including the authenticated 403 and 404 pages.

## Responsive and visual

- Test at 320 CSS pixels with no page-level horizontal overflow.
- Test browser zoom at 200%.
- Test applicable authenticated pages at 400% zoom (320 effective CSS pixels
  from the agreed 1280-pixel baseline).
- Enable forced colours/high contrast and verify focus, selected navigation,
  badges, errors and destructive actions remain distinguishable.
- Enable reduced motion and confirm no information or operation depends on animation.
- Verify text, controls and focus contrast with a contrast analyser.
- Verify statuses remain understandable without colour.
- Verify frequent touch targets are at least 44 by 44 CSS pixels.

## Calendar fallback

- Disable JavaScript and confirm public-calendar links and explanatory text remain available.
- Confirm calendar controls have natural pt-PT labels and can be operated by keyboard.
- Confirm source failures produce an accessible inline warning.

## Language

- Review all rendered text for natural European Portuguese.
- Reject the banned pt-BR terms listed in the specification.
- Verify dates and times use Europe/Lisbon and the intended pt-PT format.
