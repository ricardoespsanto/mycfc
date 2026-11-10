# Shared kayak/canoe guidance — local verification

Status: implemented locally; the complete automated two-plan browser replay passes.
Human coach/screen-reader checks remain NOT RUN, accepted as follow-up coverage by Ricardo.
Natural midnight return remains separate and unwaived; see the acceptance record.
No GitHub publication, merge, deployment or live database action is established.

## Delivered behavior

A shared water workout retains its common repetitions, nested recovery and
explicit cadence. The coach selects a permitted reference explicitly, using
the existing block adaptation for athlete/crew differences. Canonical sport
and craft permissions determine eligibility; age and group names do not.
Ambiguous or unavailable guidance refuses publication rather than choosing
implicitly. Published instructions retain the selected reference revision and
literal zone facts, including intentional overlapping ranges, `<60`, `Máx`
and `RP`. The two supplied canoe tables stay distinct.

The fresh baseline includes the reference guards and reader generation 5.
Existing-record conversion, backfill, legacy-upgrade compatibility and
historical-data preservation exercises are outside the owner's delivery scope.
Disposable acceptance fixtures are sufficient. Authentication, privacy and
application guards remain enforced. Actual season dates are configurable and
unknown; temporary placements return to the exact previous group.

## Evidence inspected by the delivery lead

- All Go tests and focused vet pass on integrated source commit `5ebbff4`.
- Six focused proper-role PostgreSQL data tests pass (21.736s): explicit craft
  permissions, complete revision facts, stale/reference guards and reader-floor
  refusal.
- Three application PostgreSQL tests pass (15.714s): common nested workout
  content with recipient-specific guidance, canonical publication, stale
  unused zone/permission refusal and atomic dated crew behavior.
- The dated variation and weekly-load regression passes (3.616s): authorized
  weekly-load edits retain the original UserID API; forged administrator input
  cannot widen scope; actual variation creation checks its bound creator.
- Native reference rendering and selection tests pass; browser asset unit
  tests pass. The OMIT selector handles the added reference field.
- Bounded independent security review found no material security/privacy
  blocker. Its separate functional weekly-load finding against intermediate
  query V4 was corrected in V5 and covered by the actual regression above.

The composed immutable runtime contains 196 verified files. Its manifest SHA256
is `29b15b2483eb51340a37df33ddbb47db95dad6b163f2c2f36758008a3deea104`.
Private local evidence is held under `/tmp/mycfc-402-*`; these paths are
execution evidence for this workspace, not a portable CI guarantee. Prior
unchanged placement, reuse and publication checks are retained separately.

## Acceptance still being executed

The fresh-setup browser case passed on 10 November 2026 (77.195s). Its
read-back confirms two groups, six memberships, six sport selections, eight
craft selections, three profiles and all 27 literal zones, created through
the UI. The same attempt did not complete the plan/reuse/zoom cases because
of test selectors and incompatible browser launch options. Those failures are
retained and corrected in the test harness; this is not a whole-run pass.

Both supplied 9–15 November reference plans are being replayed through the UI
in the approved synthetic week 16–22 November 2026. This preserves reference
versus replay dates honestly and does not infer actual season boundaries.
Target coverage is clean setup, all 33 entries, three reference profiles and
27 literal zones, copy/edit, explicit publication, athlete/current-guardian
access, temporary departure and explicit destination/return publication.
Historical fixture preservation is not an acceptance gate.

Automated browser operation is not coach-observed usability or screen-reader
use. Genuine browser zoom, forced colours and narrow-screen/keyboard results
will be reported from actual runs; natural-clock boundary and manual evidence
will not be claimed without observation.

## Narrow-screen action correction

Actual replay screenshots exposed single-character wrapping in the five weekly
header actions at 320px. A CSS-only correction reuses the existing 8rem mobile
action basis. Saved actual application HTML was rendered with before/after CSS
at 320, 640 and 1280px: the 320px actions now form readable rows with 44px
height, with no horizontal overflow; desktop dimensions are unchanged. The
delivery lead verified the frozen files and visually inspected the result.
This is targeted saved-HTML visual evidence, not a repeated full application
replay or manual screen-reader observation. JavaScript is unchanged.

## Real-plan integration defect found by replay

The sixth isolated attempt saved all 16 entries of the first reference plan
through the UI. Its first native canoe-reference adaptation then returned 409.
The PostgreSQL trace identifies `training_schedule_write_unsupported` in
`training_schedule_assert_plan_v1`: the store's CreateTrainingVariation wrapper
still used the old exact-schedule guard, despite the supported query accepting
dated placements. Earlier direct-query/application proofs did not establish
that wrapper's support for a whole start-only/rest-band week.

This is a required source fix, not a fixture or data-migration problem. The
correction must use the existing admitted modern write path and retain its
authority, locking and reader-floor protections; reference times must not be
invented to satisfy the old guard. Complete plan/publication acceptance remains
unproven until the corrected store and full UI journey pass.

The corrected Create/Retire store now uses the existing admitted publication
transaction and current actor/recipient protections. Partial-enabled weeks use
the modern path; partial-disabled operation retains the old guard. A real-store
PostgreSQL regression passed (5.192s): create, withdraw and explicit replacement
on START_ONLY with BAND/DAY siblings, unauthorized/forged-admin/stale refusals,
and byte-equivalent schedule aggregates. Both handlers return neutral 503 on
unknown commit. Integrated all-Go tests and vet pass again. Full native replay
of the corrected source remains pending.

## Final automated replay

The corrected 197-file runtime completed all four cases in 413.083s, with no
skips or retries: both plans/all 33 entries and 99 initial prescriptions, explicit
K/C/BOTH guidance, copy/edit, current temporary withdrawal and deliberate
destination/return publication, current private reads, forced colours and genuine
200% browser zoom. Six axe scans have no violations. The delivery lead checked
all 33 source/replay mappings and recomputed 16 return-publication identity/body
pairs. See `coach-planning-acceptance.md` for scope and remaining human evidence.
