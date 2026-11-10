# Saved-workout editing and cancellation

Local source verification for #391/#399/#400; not full epic acceptance or a
release record. The focused week now links to scoped editing and cancellation
tasks. Editing keeps the session and retained child identities, stages reorders
without position collisions, and removes only explicitly reviewed subtrees.
Saving changes a draft; publication remains separate.

The signed review binds the original complete raw source, proposed graph,
actor/context, plan version and selected reference facts. A changed own source
requires reload while preserving copyable own input and proof. An unchanged own
source with a changed plan may deliberately re-preview. Post-rollback recovery
rechecks the complete current scoped source before offering that action, including
serialization conflicts. Revoked access returns not-found.

The existing transaction helper, admission/runtime protections and actual
actor/grant, plan/content and profile locks remain enforced. The final database
clock checks the locked **original** session: timed starts must still be future;
untimed dates must be after today in Lisbon. Today’s untimed policy remains
unconfirmed. Existing native metadata and nulls are preserved. Minute controls
refuse unsupported seconds/fraction schedules rather than round them; status-only
cancellation can still handle such a future session. Cancellation requires the
original version, reason and explicit confirmation, and preserves content/history.

An inactive intensity revision is explicitly labelled and can stay only on its
same original block. New selections remain active-only. Active or retired
variation references prevent removal. No publication, prescription, outcome or
routine is rewritten. No schema change or existing-record conversion was needed.

## Verification

- Full Go tests and vet passed on the assembled source. Pinned SQL/templ
  generation, focused parser/render/ingress tests and independent QA/security
  reviews passed. QA’s post-read source-race finding was fixed and re-reviewed.
- Ten fresh proper-role SQL tests passed (34.897s), with three race tests
  (11.701s). Composed store tests passed (6.898s), as did actual
  `app.New`/SCS/CSRF tests (4.665s). They cover stale classification, rollback,
  original-clock eligibility, scoped profiles/removals and native recovery.
- Nine actual Chromium cases passed in 94.364s, with no skips, retries, flaky or
  unexpected results: the six CREATE regressions plus native complete mixed
  editing, own-source versus plan drift, and civil editing/cancellation.
  Seven axe scans reported zero violations. Native no-JavaScript 320px controls,
  error focus, layout, edit-back and explicit save were exercised; axe ran in
  JavaScript-enabled contexts. Fourteen screenshots were retained.
- Real relational facts prove retained IDs/parents/creation metadata, exact
  subtree removal, one new exercise identity, water/gym/activity ordering and
  unchanged nonempty publication, prescription/hash, outcome, routine and
  provenance sentinels. Root independently checked those facts and source hashes,
  inspected the narrow preview, and confirmed a reachable Docker daemon plus
  genuine absence of the exact owned containers/volumes after cleanup.

Runtime manifest: `/tmp/mycfc-391-workout-edit-domain-freeze-v3/manifest.json`,
SHA256 `49afafc56ab5207abbba3db6b8d33cff929c6171cbd7a9db619cd583210cd07c`.
Final test manifest: `/tmp/mycfc-391-workout-edit-browser-final-v1/manifest.json`,
SHA256 `409252eda2b3d8031c5b1620f41245d6ab55f39e3ee5e3d84d972911fbc75686`.
Raw evidence: `/tmp/mycfc-workout-edit-browser-0abc0f7f646d46ff-attempt5`;
root check: `/tmp/mycfc-391-workout-edit-browser-root-final-verification.json`.
The reusable runner is `e2e/run-workout-edit-authoring.py`; its `--help` describes
the required immutable runtime manifest, fresh owner name, evidence path and port.
Earlier failed runs and their corrections remain recorded in the private browser
report; they are not counted as passes.

Fixtures, dates, cadence and history sentinels are synthetic. Exact 2026/27 season
dates remain unknown/configurable. This does not establish manual assistive
technology, coach usability, natural-clock or real-plan acceptance. Copy/reuse,
dated moves with previous-group return, craft guidance, partial-plan publication
and full #391 acceptance remain separate work. No GitHub, deployment or existing
database operation occurred.
