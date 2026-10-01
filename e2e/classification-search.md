# #338 name-search/person-selection evidence

This is a source-only vertical slice, not deployment or full #338. The approved task permission is **name-only selection of any eligible active registered person** by an active administrator or active programme-granted coach. It does not grant profile, guardian-detail, history, sporting-selection or option-age access without their existing independent authority/anchor.

## Verification performed

- Base: `1c18f597977ae94406cdeb8168d9c97915032e9b`; new isolated worktree/branch `mycfc-338-selection` / `feat/338-person-search`. Integration checkout was clean and was not edited.
- PostgreSQL 16: a separately named disposable `mycfc-338-selection-pg` container on loopback port 55438, with separate fresh-baseline SQL/HTTP and browser databases. No shared Compose stack, production database, migration, or guardian/privacy activation was used.
- Watched the unknown/inactive-subject regression fail before the fix: GET returned 200 with generic “Pessoa”; POST returned 422. After the fix, unavailable subjects return identical 404 bodies before validation. Navigation/change-person tests likewise failed before those UI changes. A direct classification correction regression also reproduced acceptance of a dependent without current guardian authority before adding the task-specific transactional correction guard.
- SQL/HTTP coverage: literal and bounded name matching; blank/no-membership search; 20+5 pagination with no duplicates; name/ID-only projection; explicit selection and persisted first assignment; unknown, inactive, erased and dependent-without-current-guardian equivalence; ordinary-member and team-only denial; forged form identifiers; wrong-programme service write denial; grant revocation between search, detail and POST and direct service write; classification-only sporting correction rechecks registered-subject eligibility/current programme anchor transactionally while the generic team-scoped service remains unchanged; retained empty/error states. Existing cross-option masking, ordinary/mismatched age assignment, Polo, date/history and audited sporting correction regressions passed.
- `go test ./...`, `go vet ./...`, `npm run lint:js`, and `git diff --check`: passed.
- Focused SQL/HTTP command (passed both packages):

```sh
TEST_DATABASE_URL='<disposable PostgreSQL URL>' go test -p 1 -tags integration \
  ./internal/db ./internal/handlers \
  -run '^Test(Classification|Dated(Coach|Polo|Participation(Database|History|Recorded|Postcondition))|OrdinaryDated|AgeException|SportAssignment)' \
  -count=1
```

The first broader `-run '^Test(Classification|Dated|SportAssignment)'` attempt hit the existing `TestDatedParticipationApplyBaselineFromRecordedExpand` test's missing `mycfc_meta.schema_migrations` relation on the raw fresh baseline. That migration-ledger test explicitly requires a recorded-expand predecessor fixture, not this reset baseline. It was excluded from the subsequent focused passing command, **not fixed or claimed covered**. This slice changes no schema/migration/generated SQL.

## Browser evidence and reproduction

Provision a **new disposable** PostgreSQL database from `internal/db/schema.sql`, then apply `e2e/classification-338-seed.sql` and `e2e/classification-search-seed.sql` with `psql -v ON_ERROR_STOP=1`. Start this worktree's server with ordinary local test configuration but a distinct `DATABASE_URL`, `PORT`, `BASE_URL` and local legal URLs. Point it only at that disposable database. Use `E2E_BASE_URL` to prevent Playwright from starting the shared default server. Run as the host UID/GID with a dedicated output directory; do not write to the existing root-owned `test-results` artifact:

```sh
docker run --rm --network host --user "$(id -u):$(id -g)" \
  -e E2E_BASE_URL=http://127.0.0.1:18438 \
  -e E2E_CLASSIFICATION_338=1 \
  -e E2E_JSON_OUTPUT=/src/.338-evidence/results.json \
  -v "$PWD:/src" -w /src mcr.microsoft.com/playwright:v1.63.0-noble \
  npx playwright test \
    e2e/classification-search.spec.mjs \
    e2e/classification-338.spec.mjs \
    e2e/classification-age-exception.spec.mjs \
    --output=.338-evidence/artifacts
```

Actual result: **7 passed; 0 skipped, unexpected or flaky**. Serious/critical axe checks passed in the tested states. The new journey exercised real coach login and navigation → blank search → keyboard query/submit → explicit keyboard selection → first save → retained-query change-person navigation → empty state. At 320px it asserted no horizontal overflow, actual focus and visible keyboard outline; no page errors were reported. A second real journey saved a first Polo assignment and verified the existing shared-team name in the history. Existing tests exercised administrator invalid-date focus, simulated 200% CSS zoom, sporting correction cascade, anonymous/revoked/outsider rejection, cross-site POST rejection, and a reason-recorded age exception without reason disclosure in history.

JSON and mobile search/saved screenshots were moved out of the worktree to the profile scratch evidence directory `cache/scratch/mycfc-338-selection-evidence` (under `/home/ricardoes/.hermes/profiles/mycfcproduct`). The search screenshot was visually inspected: native controls and explicit selection remained readable and unclipped at 320px. Evidence is local and ephemeral, not a tracked production artifact.

## Exact remaining gaps

- No full `make verify`, MinIO, Terraform, release/deployment or recorded-expand migration-ledger test was performed for this source-only, schema-unchanged slice.
- No manual screen-reader, real browser zoom, Firefox/WebKit or assistive-technology acceptance. The automated zoom is CSS simulation.
- Broader #338 exclusions remain: cross-programme coach transition of an existing interval, manual multi-team assignment beyond the single Polo team, configurable taxonomy management, sporting-selection optimistic stale-version preview/retry, and complete dated-participation audit/provenance. No referral/approval workflow or cross-coach matrix was added.
- `npm ci` reported two existing pinned dependency vulnerabilities (one moderate, one high); this slice does not change dependencies or attempt an unrelated dependency upgrade.
- The erased-principal SQL fixture uses an isolated superuser connection's replica mode only to bypass its retired execution-provenance foreign key; real user CHECKs and all application authorization queries remain active. No privacy erasure implementation/activation is introduced.
