# Member detail page migration

Issue #64's member-management slice completes the shared authenticated page
contract for `/admin/membros` and `/admin/membros/{id}`.

## What changed

- The detail view now uses the River Clubhouse page header, module, section
  heading, record-list, badge, disclosure, error-summary and form-action
  primitives.
- Account type, state, email, tutor and minor access identifier are grouped as
  identity information; the signed-in administrator and active member subject
  remain visible in the shell.
- Credential issuance, account deactivation and programme membership remain
  native server-rendered forms. Risky and secondary actions are disclosed only
  when requested, while validation reopens the relevant panel.
- The established “Inscrições ativas” wording and no-JavaScript workflows are
  preserved.
- The member table's action heading and the search form were corrected so the
  route does not create page-level horizontal scrolling at 320 CSS pixels or
  200% zoom.

## Review evidence

- [Desktop member detail](member-detail-evidence/after/desktop.png)
- [Mobile member detail](member-detail-evidence/after/mobile.png)
- The pre-migration implementation is retained in Git history and used legacy
  `dashboard-hero`, `dashboard-card`, `inline-disclosure` and
  `disclosure-card` patterns without the shared page-family hierarchy.

## Verification

- `PATH="$PWD/bin:$PATH" make generate-fast`
- `go test ./internal/handlers ./ui/pages ./ui/components`
- `make verify-foundation`
- Focused admin visual journey: desktop/mobile screenshots, 320 CSS pixel
  reflow, 200% zoom, keyboard focus, and axe serious/critical checks — passed.
- Focused administrator news/member-deactivation journey with JavaScript
  disabled — passed.
- Full `CI=true make test-e2e` exercised the migrated membership journey and
  exposed both legacy locator contracts, which were updated. A final full run
  was interrupted only by the independent time-sensitive event pagination test
  after the container clock advanced during execution; that event journey had
  passed in both preceding full runs before this final assertion-only change.
