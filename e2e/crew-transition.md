# #337 / #339 disposable browser acceptance

Tests-only companion to checkpoint `80cfaef21644ccc1876ae9a20c8c8043eea1721d`.
No application, Go, schema, existing classification spec, or existing seed changes.

## Route/capability trace

- Existing staff UI is implemented, not a test-created surface: `/admin/treinos/estruturados#training-variations` renders the native **Novo alvo de variação** dialog from `ui/pages/structured_training.templ:837–955`. It submits to `/admin/treinos/estruturados/variacoes/grupos`; `StructuredTraining.CreateVariationGroup` validates craft, exact cardinality, duplicate/malformed membership IDs, explicit end/competition/open-ended exception and group capability. `PostgresStructuredTrainingStore.CreateTrainingVariationGroup` rechecks dated composition/scope. Programme-granted coaches can select their group; member accounts/revoked coaches cannot manage it.
- Classification next-day submission reuses `/equipa/classificacao/{id}` and its genuine dated participation writer. Its helper is separate from crew/history helpers so the independently owned interval-preview confirmation flow can be integrated without touching crew/history cases.
- Effective membership guards `/dashboard/initiation` and `/dashboard/leisure`. Events use `/events?view=past`, `/events/{id}` and `subject_user_id` for verified dependants.
- Real private training publication submits `/admin/treinos/estruturados/semanas/{id}/publicar`. Historical training uses `/treinos/estruturados`, `/treinos/prescricoes/sessoes/{id}` and `/treinos/prescricoes/{id}`; persisted viewer authorization is exercised, not mocked.

## Run

Prerequisites: Docker, Go, Python 3, and the checkout's exact locked Node dependencies.

```sh
npm ci --ignore-scripts
python e2e/run-crew-transition.py "$TMPDIR/crew-transition-$(date +%s)"
```

Choose a **new evidence directory** for each run. The runner refuses existing paths, starts one uniquely named PostgreSQL 16 container with `Europe/Lisbon` database timezone, a fresh named `mycfc_crew_transition_test` database, full baseline and exact migration inventory, then loads only `crew-transition-seed.sql`. It compiles the unchanged server into evidence, assigns isolated loopback ports, and runs Chromium using pinned `mcr.microsoft.com/playwright:v1.63.0-noble` as the host UID/GID. Source is mounted read-only. One worker, no retries. No Compose/shared reset, test endpoint, mocked HTTP, or fabricated publication/snapshot.

The four specs run in order:

1. `crew-transition-publish.spec.mjs`: actual administrator UI publication of historical prescriptions.
2. `crew-transition-submit.spec.mjs`: scoped coach native C2/K2/K4 creation, six forged/invalid authenticated HTTP submissions, genuine equal-priority variation conflict and explicit retirement, actual administrator next-day transition submission.
3. `crew-transition-history.spec.mjs`: separate date-boundary fixture subjects, real post-boundary future publication, member/guardian historical access and former-future denial, independent coach access.
4. `crew-transition-revocation.spec.mjs`: same saved authenticated guardian/coach cookies after authority revocation, with event/prescription/crew-management denial.

These specs intentionally opt out of unrelated default runs unless `E2E_CREW_TRANSITION=1`; the isolated runner sets it, and asserts exactly **10 passing cases**, one passing attempt each, with no skips/flakes/failures.

## Date-state distinction and invariant checks

**UI submission is not clock advancement.** A distinct adult subject (`…015`) genuinely submits a tomorrow transition through the current staff UI. Separately, after historical publication and crew edits, SQL closes two other synthetic subjects' Leisure intervals yesterday and inserts Initiation intervals effective today. This represents the effective date without changing the app clock, disabling triggers, modifying immutable starts, or claiming the UI accepts backdating. Guardian verification/revocation and historical outcomes are explicit state seeds, not UI verification/outcome-reporting claims.

The runner compares all fixture events, responses, historical session outcomes, sessions and historical prescriptions (including complete JSON snapshots, IDs and stored SHA-256 values) before crew edits, after crew edits/variation resolution/UI transition, after effective state seeding, after future publication and after revocation. Real attempted snapshot/publication UPDATEs must raise the existing immutable-trigger error; their transaction subblocks roll back. The runner also verifies:

- exactly three crew records; Beta belongs to all three overlapping crews;
- maximum effective participation cardinality remains one;
- UI-transition subject has two participation intervals;
- six retained historical prescriptions and three genuinely published future prescriptions;
- zero future recipients for the two effective former-scope subjects.

## Executed evidence

Final run: `/home/ricardoes/.hermes/profiles/mycfcproduct/cache/scratch/crew-transition-final`.

**10 passed; zero skipped, unexpected or flaky; zero retries.** Phase breakdown: publication 1, submit 4, history 3, revocation 2. The summary is parsed from each real Playwright JSON report, not inferred from terminal text.

- Keyboard Enter opens the existing modal, native initial focus is on its name field, Tab follows the group/type controls, Space selects athletes and retains checkbox focus. C2/K2/K4 mobile selection states and conflict/historical-prescription states have no serious/critical axe violations.
- Each saved crew renders its craft, inclusive start/end and composition; K4 contains all four athletes, and Beta overlaps all three. Invalid craft C4, cardinality, duplicate/malformed/unknown membership IDs and out-of-scope group submissions are refused with application `Pedido recusado` / `Acesso recusado` pages; no invalid record appears in DB totals.
- Two equal-priority C2/K2 changes for Beta visibly conflict and remove the publication form. Explicitly retiring the K2 variation removes the conflict and restores publication availability. Previously published prescriptions remain unchanged.
- Effective-date member reaches Initiation dashboard (200), loses Leisure dashboard (403), retains the navigable event archive and direct historical event URL (200), loses former future event and private-training URLs (404), and reaches the new-scope event (200). Future private-training URL has an independently authenticated in-scope positive control, not a nonexistent-target denial.
- Member and current guardian retain direct historical published-training access. Guardian's same cookies lose direct historical event/prescription access after relationship expiry. Coach keeps independent scoped crew access after the athlete moves, then the same cookies are denied after grant revocation. An unrelated member is also denied staff management.
- DB before/after equality: 3 events, 2 responses, 2 outcomes, 2 sessions and 6 historical prescriptions. All six initial snapshots retain stored SHA-256 `95933a0a89dd29c97cfb6447236f7458dad0ba0d888e4aeba99e49a278024db5` (same base prescription for these synthetic athletes). This is persisted checksum invariance, not a claim that jsonb reserialization reproduces the original Go hash bytes.
- `summary.json`, four phase JSON/logs, `retained-before.json`, `retained-after.json`, `immutability.log`, `fixture.json`, screenshots and `cleanup.json` are retained. The K4 screenshot was visually inspected: dates, checked athletes and the focused-checkbox ring are readable at mobile width; the dialog scrolls to the selected athletes. Athlete/group labels are adjacent, so this is not a manual typography or screen-reader acceptance claim.
- Cleanup verified: app stopped, isolated containers absent, loopback ports 56715/46317 closed. All evidence is host-owned. Saved sessions are synthetic; evidence is created mode 0700 and should not be published as a general test artifact.

## Limits and integration dependency

This closes the dedicated browser evidence gaps for the exercised #337 crew and #339 programme-transition/history journeys, not every epic or release gate. It does not claim manual screen-reader/real-browser zoom acceptance, team-specific boundary coverage beyond existing DB/HTTP tests, general backdated corrections, legacy migration, privacy erasure/restore, or production release. No behavior bug was reproduced in these exercised paths; early local failures were fixture/locator expectations corrected to the traced existing contract, not production edits.

The upcoming #338 interval-preview writer must adapt `submitTomorrowTransition` to the new native preview/confirm interaction, retaining the real POST, successful date/person/programme confirmation and DB/history assertions. No crew/history helper depends on that confirmation flow. Re-run this isolated runner after integration; this source-only result is not a final integrated `make verify` claim.

`npm ci`/`npm audit` report two pre-existing transitive dev-dependency advisories (`brace-expansion`, high; `fast-uri`, moderate). Dependencies were not changed in this tests-only slice. Full `make verify` was not run.
