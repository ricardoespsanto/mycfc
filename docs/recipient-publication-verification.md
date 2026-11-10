# Recipient review: fresh-local increment

This #391/#403 increment lets a coach review a group/week's current authorised
recipients before explicitly confirming publication. It is local source and
verification, not deployment, whole #403 acceptance or completion of #391.

## Behaviour

The first POST previews only, including old forms without an intent field.
Confirmation binds the actor, plan, original source/latest revision, summary,
return context and complete canonical recipient/instruction set. A concurrent
change requires a fresh review; errors retain the coach's own original proposal
without showing stale recipient names. Unknown save outcomes offer history and
review links without automatic resubmission or a claim that nothing was saved.

A fresh unchanged review creates no new revision, including an edit followed by
reversion. An empty first publication is blocked; a deliberately reviewed empty
successor can remove recipients. Existing prescriptions, hashes and outcomes
remain immutable. Current staff, athlete and guardian permissions are checked
separately from stored history.

The baseline includes the necessary schema/query changes for fresh databases.
Existing-record conversion, backfill and legacy upgrade/controller rehearsals
are not delivery gates. Optional work is preserved separately. No existing or
production database was reset, and no guardian intake was enabled outside
disposable test fixtures.

## Verification

- The assembled source passed `go test ./...` and `go vet ./...`.
- Seven local configuration tests passed with
  `python3 scripts/local-db-command_test.py`; shell syntax and diff checks passed.
- Proper-role PostgreSQL tests cover canonical publication, stale reviews,
  no-ops, empty successors, revoked/expired dependent eligibility, concurrency,
  exact artifact limits, atomic refusal and unchanged history.
- `TestFreshRecipientPublicationActualApplicationSessionCSRF` exercises actual
  `app.New`, login, PostgreSQL SCS sessions, same-origin/CSRF enforcement,
  rendered forms and the real store. Its exact-minimum run passed in 4.138s.
- `TestLocalRecipientConstructiveBootstrapFloorRealPostgres` passed in 3.632s:
  fresh provenance capture, intended database identities, explicit local floor
  preparation, original LOGIN restoration and refusal of foreign adoption or
  another database's active publisher connection.
- The focused browser runner passed two Chromium cases in 14.9s without skips
  or retries: JavaScript at 1280px and native forms without JavaScript at 320px.
  It used actual Go bootstrap, migration, local floor preparation and a low-role
  app on a newly created disposable PostgreSQL cluster. Native multiline
  confirmation, no save at preview, unchanged no-op state, real UI edit/stale
  refusal, explicit re-review, successor lineage and history were observed.
  Three axe analyses found no serious or critical violations. Owned resources
  were removed; the delivery lead independently confirmed absence against a
  reachable daemon.
- Independent presentation, persistence, fresh-local and composed security/QA
  reviews found no material runtime blocker. QA's outdated browser-consumer
  finding was addressed in auth, week and crew helpers; those broader suites
  were syntax-checked but were not rerun for this bounded increment.

Source and test hashes were unchanged during the final browser run. Its evidence
is retained privately under `/tmp/mycfc-recipient-browser-a86b9ad87a18-final`.
The test seed uses explicitly synthetic configurable season dates, not inferred
2026/27 club boundaries or the supplied private plans.

## Repeating the focused browser check

The opt-in runner refuses existing named resources. Supply a unique name, free
loopback ports, an evidence directory outside the checkout, and a SHA256 inventory
of the source being tested through `--runtime-manifest`. That inventory is JSON
with a `files` map of repository-relative paths to SHA256 values; the runner checks
it before and after execution. The delivery run combined the immutable runtime
and fresh-local inventories. Use only a genuinely new disposable target; existing
reset helpers are not permission to erase an existing/default stack.

```sh
python3 e2e/run-recipient-publication.py EVIDENCE_DIRECTORY \
  --name mycfc-recipient-browser-UNIQUE_ALPHANUMERIC_SUFFIX \
  --pg-port FREE_PG_PORT --app-port FREE_APP_PORT \
  --runtime-manifest SOURCE_MANIFEST_JSON
```

## Limits

The 4096-tuple, 32MiB source/output and 200000-byte PostgreSQL artifact limits are
refusal ceilings, not promised successful task capacity. An actual 4096-tuple
confirmation reached the 10s transaction deadline and left no publication rows.
A 32MiB source preview sampled about 219MB live Go heap; it is not a peak-RSS or
production-concurrency measurement. Oversized artifacts and deadline failures
refuse the whole publication.

Resolved per-athlete workout/craft preview, partial-time authoring, dated moves,
temporary return, the two complete private plans, coach/manual accessibility and
natural-clock acceptance remain later #391 work. Season boundaries remain unknown
and configurable. No merge, publication, deployment or live operation is implied.
