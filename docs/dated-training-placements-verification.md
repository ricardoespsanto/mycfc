# Dated training placements — local verification

This implements the local #396/#397 placement and publication increment. It is
not a merge, deployment, whole-#391 acceptance or a real club-season approval.

## Observed behaviour

Coaches use explicit configured season/date search and native placement tasks.
Initial roster additions use a signed, bounded selection; an existing placement
does not offer another indefinite JOIN. A temporary move reviews and records the
exact previous group for the calendar day after its inclusive end. Cancellation
before effect restores only the reviewed source and its untouched legs. Original
proof replay makes no additional writes; competing changes require new review.
Unknown commit results offer current-state recovery without claiming rollback or
automatically retrying.

Publication binds the actual person, participation episode, session and placement.
A move withdraws future instructions from the group being left. Returning to A2
does not revive A1 instructions: deliberate republication creates a new
placement-bound prescription even when its body, hash, episode and session match
A1 exactly. Stored prescriptions and outcomes remain unchanged. Current subject,
guardian and staff rights remain independently enforced.

An early COMPLETED report does not exempt a withdrawn future instruction. The
completed-history exception requires a strictly earlier Lisbon calendar day;
same-day withdrawn completed bodies remain outside that exception while the
owner's started/untimed-today boundary is unresolved. Own stored feedback is
separate. Only affected moves are held; independent future moves can proceed.

## Verification

- All Go tests and vet passed on the composed implementation. Pinned sqlc
  regeneration retained the existing query methods, including contextual reuse. The existing guarded local
  preparation command passed real isolated PostgreSQL verification at reader4
  (4.696s), including fencing, exact intent recovery and refusal of a future floor.
- The exact final persistence source passed 18 proper-role PostgreSQL tests
  (67.813s): dates/return/cancellation/replay, uniqueness, current delivery and
  guardians, nullable candidates, physical head/version witnesses, locking,
  classification continuation, fresh initialization and privacy protections.
  Populated dump/restore evidence exercises the approved private relational
  primitives; it does not establish external activation/controller acceptance.
- Eight application PostgreSQL tests passed (42.258s), plus scope/classification
  tests (9.325s). These use actual signed JOIN/LEAVE and the application role.
  They cover 20-person selected-set replay, same-body A2 publication and the
  unchanged pre-completed-future regression failing before and passing after
  the withdrawal correction.
- Independent Go/UI QA found no additional actionable defect. Independent Data
  security review identified three P2 findings; all were corrected and closed
  against immutable V4. A separate composed application security pass found no
  additional material finding.
- Actual Chromium passed both journeys in 57.827950s, with no skips, retries or
  flaky tests. The 320px JavaScript-disabled journey covers JOIN, invalid-date
  feedback, original-proof Back, temporary review/confirmation, stale conflict,
  exact replay and pre-effective cancellation/replay. The JavaScript journey
  covers current-effective A→B→A2, old future reads returning 404, explicit B/A2
  publication, isolation and unchanged stored history. Six axe scans reported
  zero violations; 11 screenshots and 11 HTML captures are preserved.

The focused browser fixture contained nonempty publication/prescription history
and empty outcomes. Nonempty outcome/feedback preservation is established by
the PostgreSQL evidence, not claimed from that browser fixture. Initially
revoked browser controls and actual PostgreSQL authority revocation are distinct.

The first browser attempt exposed a test assumption demanding JOIN proof for an
already-placed person; the second exposed a nonexistent DOM selector. Both
failures and their narrow test corrections are preserved. Application runtime
bytes were unchanged across all three attempts. The delivery lead independently
verified all 188 runtime files, nine test inputs, the binary, 12 read/replay/stale
snapshot comparisons, persisted return/cancellation and A2 lineage. Docker was
reachable and all nine exact owned resources from the three attempts were absent.

## Exact private evidence

Runtime manifest: `/tmp/mycfc-391-placement-composed-source-v1/manifest.json`,
SHA256 `989f2e5475ba8350a02985e46dcc330faf4f2e79adc4b879c763797edf44e8d4`.
Binary SHA256:
`7842dec57ac6727982d71f78810d4b3a08a367732e03445f890ccd0951f407a2`.
Final browser handoff: `/tmp/mycfc-391-placement-browser-final-v1/manifest.json`,
SHA256 `f3d7b07ecac9beeacfc9c8c4a704114da4340b3d0341a1544b916ef38043930c`.
Root reconciliation: `/tmp/mycfc-391-placement-browser-root-final-verification.json`.
Actual final attempt:
`/tmp/mycfc-391-dated-placements-browser-be331de3a1db12a0-attempt3`.

Data source and terminal evidence are frozen separately at
`/tmp/mycfc-391-placement-final-source-v4/manifest.json` and
`/tmp/mycfc-391-placement-terminal-evidence-v4/manifest.json`. Required application
test evidence is at `/tmp/mycfc-391-placement-domain-tests-v3/manifest.json`.
Independent reports are `/tmp/mycfc-391-placement-data-security-final-review.md`,
`/tmp/mycfc-391-placement-composed-qa-review.md` and
`/tmp/mycfc-391-placement-composed-security-review.md`.

## Remaining acceptance and scope

Season dates were explicitly synthetic; actual 2026/27 boundaries remain unknown
and configurable. Current-effective changes used the real application clock,
but no natural midnight rollover was observed. Coach-observed usability, screen
reader, real browser zoom, forced colours, #402 craft guidance and both #404 real
plan replays remain outstanding. The preview repeats the withdrawal notice;
this is recorded as minor usability friction, not a safety finding.

Existing-record conversion, backfill and legacy upgrade compatibility are not
delivery gates. The complete baseline and forward schema definition implement
the new feature. All fixtures were newly owned and disposable; no existing
database reset, production operation, GitHub write or release occurred.
