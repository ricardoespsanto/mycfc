# Accessibility manual check

This checklist becomes mandatory when the first templ page is implemented.

## Keyboard and focus

- Complete login, registration, dashboard navigation, dependent creation and repair reporting without a pointing device.
- Confirm the skip link is the first focusable control and becomes visible on focus.
- Confirm focus order follows the visual and semantic order.
- Confirm every focus indicator remains clearly visible at 200% zoom.
- Confirm HTMX success and validation swaps move focus to the correct heading or error summary.

## Screen reader

- Confirm page titles and the single H1 identify each page.
- Confirm landmarks and navigation labels are useful and non-duplicative.
- Confirm required state, help text and field errors are announced with each control.
- Confirm status messages are announced once and validation errors are urgent without being repeated.
- Confirm repair-image alternative text describes context rather than filenames.

## Responsive and visual

- Test at 320 CSS pixels with no page-level horizontal overflow.
- Test browser zoom at 200%.
- Verify text, controls and focus contrast with a contrast analyser.
- Verify statuses remain understandable without colour.
- Verify touch targets are at least 24 by 24 CSS pixels.

## Calendar fallback

- Disable JavaScript and confirm public-calendar links and explanatory text remain available.
- Confirm calendar controls have natural pt-PT labels and can be operated by keyboard.
- Confirm source failures produce an accessible inline warning.

## Language

- Review all rendered text for natural European Portuguese.
- Reject the banned pt-BR terms listed in the specification.
- Verify dates and times use Europe/Lisbon and the intended pt-PT format.
