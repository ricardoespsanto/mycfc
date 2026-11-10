# Scheduling reader browser verification

This opt-in suite exercises actual application routes against an isolated synthetic PostgreSQL database. It is compatibility-stage evidence for #391, not real-plan entry, coach acceptance, manual assistive-technology testing, a natural date rollover, or release approval.

Run from the test checkout with a frozen application source:

```sh
python3 e2e/run-training-schedule-compatibility.py /tmp/new-private-evidence \
  --app-source /absolute/path/to/frozen/source
```

The runner uses the repository's exact package lock, PostgreSQL 16.9 and Playwright 1.63.0. It builds the actual server and runs its `bootstrap-db`, `migrate` and `harden-db` commands with separate synthetic bootstrap, migration and web identities. The HTTP process connects only as the non-superuser web role. It does not read a local `.env`, inherit cloud/application credentials, or contact production services. The runner removes its own server and containers and retains private source, binary, test, history and cleanup evidence.

## Scope

The ten selected browser cases cover four timing shapes across the group week, ordinary athlete training/calendar, the Iniciação programme dashboard and frozen prescriptions; typed routine display; known-start outcome and correction without filling a planned end; an independent exact-target edit in a mixed week; retained stale/unsupported submissions; revoked staff and guardian viewer access; qualified daylight-saving-fold intervals; native mobile/desktop navigation; actual cross-origin CSRF denial; and visible failure for unknown stored artifact versions. Browser runs use one worker and zero retries. A selected skipped case is a failure of the runner's final report check.

Before/after comparisons preserve every original immutable publication and prescription. The expected actual outcome and exact draft edit are checked separately. Fault-injection rows are explicitly identified rather than confused with legitimate authoring or historical mutations.

## Evidence limits

The database starts from the actual C baseline and exact 79-entry migration inventory. It then applies the explicit **test-only nullable shape fixture**, establishes a synthetic reader floor and opens the disposable database's gate through the private owner functions. This is not an embedded P migration or an actual P-ledger rollout. The web process is compile-unstamped with `APP_ENV=test`; it uses the documented local exception and therefore does not prove production admission. The separate stamped root-projection application test owns that evidence.

Guardian operational policy, approval, runtime binding and verified relationship are synthetic fixture state. The viewer test proves present permission versus revoked permission, not legal approval or the separately recorded publication-recipient eligibility fix. Original stored instructions stay intact when viewer access is withdrawn.

Unknown versions cannot be created through the supported compatibility writer. The final negative case uses the disposable fixture owner's transaction-local replication-role override to insert one new malformed prescription and replace a mutable routine snapshot. It does not alter the original eight frozen prescriptions, persisted production data, trigger modes, runtime grants or application verifier. Its result establishes authorized read failure, not acceptance of such a write.

Historical DST instants are explicit fixture inputs, not a changed server/database clock or natural date-boundary test. Native navigation, keyboard focus and overflow checks run with JavaScript disabled. Axe requires script injection, so matching actual routes are audited in separate JavaScript-enabled contexts; these are not described as axe runs inside a script-disabled page. Automated axe and overflow assertions do not replace genuine zoom, screen-reader or coach observation. The full #391 acceptance and separate publication/deployment gates remain outstanding.

## Recorded execution

The final fresh run on 10 November 2026 passed all **10 cases**, each executed once with **zero selected skips or retries**, in 26.7 seconds. The application source was clean at `77abe1a8186152e018d784d0453b300741ee57b4`. Seven actual axe analyses found **zero serious or critical violations**. Native 320-pixel and 1280-pixel screenshots, keyboard navigation and overflow checks passed. Screenshots were visually inspected for truthful timing and wrapping.

Private evidence is `/tmp/mycfc-391-precision-c-browser-final`: `summary.json`, `browser.json`, seven `axe-*.json` reports, screenshots, full source and test manifests, planning/history snapshots and cleanup receipts. Application source and the test manifest remained unchanged throughout execution.

- Source manifest SHA256: `0bef7be7ad04895277d0413d17ba24beda45dd8ae879afb0362516eb035b496b`.
- Application binary SHA256: `edf03c647fe0efa95a8107deb3a21274ea63f8cbf9436b9726b18fd0a8ef8a48`.
- Test manifest SHA256: `f9fe391d325df33f471c3052dd7cb96e335fccddeaf967dcfcafb97a5f6b501d`.

The complete planning-row checkpoint before and after denied writes was identical, including plan versions, sessions, segments, blocks, routines, copy events, publications, prescriptions and outcomes. The original publication, all eight original prescriptions and the prior outcome were unchanged. The known-start session's actual outcome was created and corrected through native forms to 45 minutes/version 2; its planned end remained NULL. Final counts were two plans, six sessions, one publication, nine prescriptions, two outcomes, one segment, one block, one routine and zero copy events. The sixth session/ninth prescription are the separately labeled malformed-artifact fault fixture, not successful authoring or unexpected publication. Runner-owned processes and containers were removed.

Earlier trials remain private and are not passing evidence. They identified fixture/selector assumptions and one actual source defect: stale ordinary-session edit recovery replaced entered values with stored values. The bounded source fix was independently tested and integrated before this final run; its routed case confirms the entered title, old start/end and original version token remain after HTTP 409. Fully revoked staff are denied by outer middleware with HTTP 403 and no retained private data; the separate source QA suite proves the later fresh-store guard's HTTP 404 behavior. Forged precision receives explicit validation HTTP 422 with unchanged database rows.

The `/today` page is an event-focused surface; this suite does not claim it displays training. It exercises the actual changed training agenda at `/dashboard/initiation`. The programme, season and authority inputs are synthetic fixture choices, not new club rules or real season dates.
