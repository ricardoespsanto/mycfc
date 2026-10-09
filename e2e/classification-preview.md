# #338 original-set protection and dated preview

Local source only; no production migration, new dependency, GitHub write or release.

## Contract

- Both sporting forms carry a signed original version bound to the authenticated actor, selected person and correction kind. The version covers **both** modalities and crafts plus append-only correction-event identities, including changed-then-reversed selections. The service checks current actor/grant/subject authority, locks the subject and compares the original version in its serializable transaction **before** selection changes or audit inserts.
- A stale/expired/forged original token returns an actionable conflict. Proposed checkboxes and reason remain visible, but the original token stays unchanged: retrying retained stale values cannot silently overwrite the latest set. Use the explicit reload/review link to discard the uncommitted proposal and review current selections.
- Dated creation and next-day transitions first show the selected person and previous/proposed programme, escalão, team and dates. The proposed end is the **actual season end** applied by the existing database trigger, not an invented open-ended interval. Only `Confirmar participação` saves.
- The preview expires in 15 minutes and is signed against actor/person and the exact proposal (including a hash of any exception reason) plus the interval/season/category/team snapshot. Editing values requires a new preview. Confirmation revalidates scope/eligibility/options and the service rechecks the snapshot transactionally after the subject lock. Expired/stale/forged confirmations retain values and write nothing.
- Version reads independently recheck active staff and active/non-erased/verified-dependent subject eligibility; opaque hashes are protected metadata too. Existing guardian-revocation tests now cover both additional version SELECTs.
- Signing uses a random process-local key; restart or routing a preview to another independent server process safely invalidates the token and requires review again. No persistent preview/reason store or schema change is introduced. No reason, version or token is added to logging.
- Existing today/tomorrow restrictions, coach programme scope, attributed age exceptions, shared Polo-team resolution, immutable historical event/training/prescription identities and the generic non-classification sporting caller contract remain unchanged.

## Automated verification

Run in an isolated PostgreSQL 16 database provisioned from `internal/db/schema.sql`; initialize the ordinary empty `mycfc_meta.schema_migrations` table for full-package ledger fixtures. This is a fresh final baseline, **not** a predecessor/forward migration rehearsal; no schema changes need forward application.

```sh
TEST_DATABASE_URL='<isolated database URL>' go test -tags integration -p 1 ./internal/db ./internal/handlers
TEST_DATABASE_URL='<isolated database URL>' go test -race -tags integration ./internal/db \
  -run '^TestSportingOriginalVersionRejectsCommittedAndConcurrentCorrections$' -count=20
```

The database regressions cover both committed correction orders, cross-axis cascade conflicts, reversed sets, two real concurrent writer orders, no stale selection/audit changes, forged/expired/changed dated confirmation, a committed interval change between handler validation and the service write, and successful explicit refreshed preview/confirmation. Existing authorization, age-exception and guardian-revocation assertions are preserved.

For browser coverage, seed a **separate fresh** isolated app database in this order:

1. `e2e/classification-338-seed.sql`
2. `e2e/classification-search-seed.sql`
3. `e2e/definitions-335-seed.sql`
4. `e2e/classification-preview-seed.sql`

Run repository-pinned Playwright `mcr.microsoft.com/playwright:v1.63.0-noble` as the host UID/GID against that isolated app, with writable evidence/output paths outside the checkout:

```sh
E2E_BASE_URL='<isolated app origin>' E2E_CLASSIFICATION_338=1 \
  E2E_JSON_OUTPUT='<isolated results.json>' npx playwright test \
  e2e/classification-338.spec.mjs e2e/classification-age-exception.spec.mjs \
  e2e/classification-search.spec.mjs e2e/definitions-335.spec.mjs \
  e2e/classification-preview.spec.mjs --workers=1 --retries=0 \
  --output='<isolated artifacts>'
```

The combined run has 13 passing cases, zero skips/retries/unexpected/flaky cases. The two new cases exercise two-tab stale sporting edits, unchanged stale retry, explicit reload, forged original token refusal, dated old/new Polo preview with the actual season end, explicit confirmation, stale second-tab refusal, retained fields, native keyboard preview/error focus, 320px reflow, forced colours and serious/critical axe. Existing search and age-exception journeys now explicitly confirm; the shared default `auth.spec.mjs` setup helper also confirms without dropping any downstream assertions.

Go tests and vet pass in a minimal environment. The final full tagged DB/handler package run has one explicit skip, `TestDatedParticipationApplyBaselineFromRecordedExpand`, because the disposable database is a fresh final baseline rather than a recorded predecessor fixture; both packages pass and no tests fail. The inherited application environment otherwise contaminates configuration tests; isolate it instead of changing those tests. The broader `make verify`/default authenticated browser gate and independent review were not executed in this bounded follow-up. Manual screen-reader/real-browser-zoom checks, separately owned crew/history browser journeys, merge/deploy/live migration remain distinct gates.
