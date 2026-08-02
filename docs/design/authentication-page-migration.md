# River Clubhouse authentication migration

Issue #77 brings login and registration into the River Clubhouse system without
presenting authenticated navigation or account context before a session exists.

## Shared contract

- `components.AuthBase` supplies a focused MyCFC identity, skip link, route back
  to the public entry point and a restrained footer.
- Both pages use the established page/raised surfaces, typography, spacing,
  border, radius, shadow, action, focus, error and forced-colour tokens.
- Login and registration use shared required labels, help text, error summaries,
  stable field-error targets and form actions.
- Login retains the submitted identifier after a failure. Registration retains
  safe name, email, date and consent choices while passwords are deliberately
  cleared.
- Autocomplete metadata covers username/current password, name/email/birthday
  and new password fields.
- Authentication, session, authorization and redirect rules are unchanged.

## Review evidence

The committed desktop and 375px mobile captures under
`authentication-evidence/after/` cover empty and invalid login and registration
states. Successful authentication continues into the representative Today
states captured by the six-persona harness.

The UI-review test asserts the unauthenticated shell, absence of authenticated
navigation, semantic validation relationships, preserved safe values, cleared
passwords, keyboard focus recovery, serious/critical axe results, 320px reflow,
200% zoom/text enlargement, forced colours and reduced motion.

## Verification

- `go test ./...`
- `make verify-foundation`
- `CI=true make test-e2e` — 9 passed, 7 UI-review scenarios skipped by design
- `CI=true make ui-review-screenshots` — 7 passed, including authentication
  states and the six authenticated personas
