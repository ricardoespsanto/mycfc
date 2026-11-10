# Focused water workout creation

This is a bounded local #391 increment toward #399, not completion of either
ticket. It adds a selected-day task for an authorised coach or administrator to
write water instructions, preview them and explicitly save an unpublished draft.

## Behaviour and boundaries

- Require an actual start. Keep an unknown end empty; an optional end needs its
  own explicit date and time. Lisbon gaps are invalid and genuinely repeated
  clocks require an explicit occurrence. Do not infer duration or season dates.
- Keep the selected group, week and day visible. Native phase commands preserve
  incomplete input, order and removals. Persist only supplied own-word phases.
- Share the existing read-only instruction renderer with the preview. Preview
  creates no draft or publication; saving creates one session, one water activity and its ordered
  phases atomically. Saving does not publish instructions.
- Bind confirmation to the authenticated actor, source context, reviewed version
  and allocated identity. Recheck current authority and the complete graph in the
  write transaction. A stale review needs deliberate re-preview; exact replay
  cannot create another workout. Unknown commit results must not trigger resend.
- Preserve existing publications, prescriptions and outcomes. Structured water
  details, gym/mixed authoring, identity-preserving editing and partial-plan
  publication remain later work.

Fresh schema 81 allows nullable endpoints while retaining the existing timing,
kind, week and history protections. It contains no business-record conversion or
backfill. The writer remains closed until explicit preparation of a proven new,
owned local fixture; the web application cannot enable it. This work does not
authorise any operation on an existing or production database.

## Verification checkpoint

The initial frozen 43-file implementation passed full Go tests and vet,
generated-file parity, proper-role PostgreSQL atomicity/rollback/replay/scope
checks, real application/session/CSRF integration and fresh local preparation.
The actual PostgreSQL artifact-size boundary and encoded request limits are
separate checks, not a maximum-success capacity claim. The local recovery test
injects a missing journal acknowledgement after real enablement; it is not a
dropped-network-acknowledgement test.

Independent composed QA and the first real browser run found two P2 defects:
ordinary times incorrectly acquired required ambiguity selectors after returning
to the editor, and expired signed reviews offered an unusable re-preview action.
The first browser attempt failed all three cases: one test-copy capitalization
error and two instances of the ordinary-time defect. Those failures and their
traces are preserved. All resources from that new disposable fixture were
removed and their absence independently checked.

Both P2s are corrected and independently re-reviewed. Ordinary times no longer
receive ambiguity controls; actual folds still require an explicit choice. An
unusable signed review retains visible copyable text and its original proof,
offers the week recovery link and suppresses an impossible new preview. Expiry,
current authorisation and stable create identity are not relaxed.

The final real Chromium run passed **3/3 in 19.0 seconds**, with no skips, retries
or flaky results. It used the actual application, session/CSRF middleware and low
web database role after genuine fresh bootstrap, schema installation, explicit
local preparation and hardening. Cases covered:

1. Native start-only preview with SQL NULL end, exact multiline/tab text,
   explicit atomic save and original-confirmation replay without another graph.
2. Native no-JavaScript 320px validation/focus, incomplete phase commands,
   explicit interval, edit-back/re-preview and one saved identity.
3. A legitimate concurrent create, stale 409, retained raw input, deliberate
   re-preview with the same signed identity and unchanged concurrent workout.

Two axe scans reported no serious/critical WCAG-tag violations. Four screenshots
were captured; the final desktop and narrow interval previews were visually
inspected. Exact database assertions showed four sessions across three synthetic
weeks and no publication, prescription or outcome writes. Full Go tests and vet
also passed on the assembled final source. The final runtime and test hashes
remained unchanged throughout execution.

A second preserved fixture attempt stopped before browser execution: the image's
temporary Unix-socket server appeared ready before its database existed. The
runner now waits for an actual nonprompting TCP SELECT against the configured
new database; it does not create or repair a database manually. Each attempt used
new owned resources. The final app stopped and the reachable Docker daemon
independently confirmed the exact two containers and volume absent.

Private evidence is retained under
`/tmp/mycfc-authoring-browser-82c6d4930fae-attempt3`; independent QA/security
amendments and root cleanup verification accompany that checkpoint. No private
coach screenshots or real subject records are included in the browser fixture.

This is not manual screen-reader/zoom/forced-colour acceptance, coach observation,
real-plan replay, a natural clock boundary, full #399/#400/#391 completion, release
or deployment. Actual 2026/27 season boundaries remain unknown and configurable.
Temporary placements must return to the previous group when that separate
workflow is implemented. Existing-record migration is not a delivery gate.
