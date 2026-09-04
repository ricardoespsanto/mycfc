# Accessibility manual check

This checklist is the maintained manual companion to the automated WCAG 2.2
AA contract. Record the browser, assistive technology, operating system, date
and tester in `docs/design/accessibility-audit.md` for every release pass.

## Keyboard and focus

- Complete login, registration, Today navigation, dependent creation, repair
  reporting, event response, training authoring and the administrator member,
  news and fleet workflows without a pointing device.
- Complete password-recovery request, email-link reset, validation correction,
  successful login and used-link handling with keyboard-only navigation.
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
- Confirm both new-password fields expose the expected password-manager purpose,
  remain empty after validation errors, and move focus to the error summary.
- Confirm status messages are announced once and validation errors are urgent without being repeated.
- Confirm repair-image alternative text describes context rather than filenames.
- Confirm member, tutor and administrator journeys in the named
  browser/screen-reader pairing, including the authenticated 403 and 404 pages.
- As an administrator, open `/admin/treinos/estruturados`, choose a group,
  week and session, and confirm that the page title, single H1, selected-plan
  context, section headings, local navigation and current session are each
  announced once and in a useful order. Open one gym task route and confirm
  that its route-level heading and plan/week/session context are announced;
  then use Voltar or Cancelar and confirm focus and the selected context return
  to the planner rather than to the start of the page.
- With a current profile photograph, open the removal confirmation from the
  member profile. Confirm that the person's name, the permanent removal effect,
  the initials fallback and the destructive confirmation control are announced
  before activation. Repeat for the applicable tutor-dependant and
  administrator-member routes when those actors are in the release scope.

## Responsive and visual

- Test at 320 CSS pixels with no page-level horizontal overflow.
- Confirm request, reset, confirmation and unavailable-link recovery states at 320 CSS pixels.
- Test browser zoom at 200%.
- Test applicable authenticated pages at 400% zoom (320 effective CSS pixels
  from the agreed 1280-pixel baseline).
- Enable forced colours/high contrast and verify focus, selected navigation,
  badges, errors and destructive actions remain distinguishable.
- Enable reduced motion and confirm no information or operation depends on animation.
- Verify text, controls and focus contrast with a contrast analyser.
- Verify statuses remain understandable without colour.
- Verify frequent touch targets are at least 44 by 44 CSS pixels.
- At 320 CSS pixels and at 400% zoom, inspect the structured planner after a
  group, week and session have been selected. Its controls, current context,
  session navigation and task-route return controls must remain readable and
  operable without page-level horizontal scrolling.
- At 320 CSS pixels, inspect the profile-photo removal confirmation. The
  subject, irreversible effect, return control and destructive action must
  remain distinct, readable and reachable without accidental activation.

## Calendar fallback

- Confirm internal agenda items expose clear loading, empty, success and failure states, including keyboard-recoverable interactive failures.
- Confirm agenda links and labels use natural pt-PT text and can be operated by keyboard.

## Language

- Review all rendered text for natural European Portuguese.
- Reject the banned pt-BR terms listed in the specification.
- Verify dates and times use Europe/Lisbon and the intended pt-PT format.
