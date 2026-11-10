# Coach planning: confirmed owner decisions

These decisions guide #391 and its dependent stories. They record product
authority, not implementation, acceptance or release completion.

## Rhythm guidance and replay authorisation

Ricardo answered the remaining #391 questions on 10 November 2026:

> 1. Yes overlap is expected. Rhythm is the difference between canoe and kayak.
> 2. permission granted. It is all test data so do what is easiest.

The owner explicitly instructed implementation to proceed: overlapping rhythm
ranges are intentional, the prescribed workout content is shared, and each
recipient uses the relevant kayak/canoe guidance from the supplied references.
Keep the two supplied canoe tables distinct. These points are answered; do not
add another values/units/common-content approval gate for this exercise.

Both reference plans may be replayed in a future synthetic week, preserving
every workout and its weekday and recording reference versus replay dates.
The selected replay is **16–22 November 2026**, corresponding to the reference
**9–15 November 2026**. Synthetic season configuration covers the exercise;
actual club season boundaries remain unknown and are not a blocker. Newly
owned disposable test fixtures may be created/recreated as needed. This does
not authorise unrelated production operations or deletion of real records.

Existing-data conversion/backfill remains outside scope. The confirmed return
to the exact previous group after a temporary placement ends remains in force.
Implementation and actual acceptance results must still be recorded honestly;
the decision itself is not evidence that a replay or manual check occurred.

## Delivery scope correction

On 10 November 2026 the owner clarified that **migration of existing database
records is not a required #391 product outcome**. Existing-data conversion,
backfill and legacy-data upgrade compatibility are not delivery gates. Preserve
the work already produced; save legacy upgrade/controller work as deferred work
rather than deleting it or continuing to expand it for this increment.

Keep the necessary fresh schema, generated queries and application integration
for the new features. Use new disposable local fixtures and proportionate real
tests. Authentication, authorization, privacy and enforced protections remain
required.

Recipient publication review, structured/mixed workout creation, editing,
contextual copying/routine reuse and dated placement/return/access integration
are locally verified. See `../dated-training-placements-verification.md` for the
placement evidence and its limits. Necessary fresh schema, generated queries,
authenticated integration and proportionate PostgreSQL/browser checks implement
these features; conversion or upgrade rehearsal of existing records is outside
this delivery scope.
Saving a draft does not publish it. Existing-record conversion, backfill and
legacy upgrade rehearsals remain outside delivery gates; their optional work
stays saved in isolated checkouts and frozen handoffs.

This correction does **not** authorise deleting, resetting or wiping any existing
or production database, discarding records, removing backups or performing live
operations. Any necessary consequential destructive operation needs a separate
decision identifying its exact target.

## Training groups and temporary moves

- An athlete has one active training group within a participation programme.
- Iniciação and Competição are mutually exclusive; a group cannot mix them.
- A placement may have a temporary end date.
- **When a temporary placement ends, the athlete returns to their previous
  group.** The owner confirmed this on 10 November 2026. This replaces the open
  return-versus-unassigned question for #396/#397.
- When a move takes effect, withdraw access to already-published future
  instructions from the group being left. Destination instructions require
  explicit publication. Returning to the previous group does not revive
  withdrawn instructions or publish automatically.
- Preserve stored history; permission to read that history is checked separately.

The dated-move implementation must include the return in the reviewed timeline,
preserve one effective placement, and validate current eligibility and conflicts.
This decision record supplies policy authority; implementation verification is
recorded separately in `../dated-training-placements-verification.md`.

## Season dates

**The exact start and end dates of season 2026/27 are unknown.** The owner said
that the dates vary and supplied no approved boundaries on 10 November 2026.

Keep actual dates configurable for each season. Do not infer boundaries from
the label, substitute approximate dates, or describe synthetic test dates as
approved club dates. The supplied 9–15 November weekly plans establish their
week, not the season's boundaries. Continue implementation and clearly labelled
synthetic tests that do not depend on those missing club dates; real-season
acceptance must retain this evidence gap.

## Reconfirmed scope and next increment

The owner reconfirmed that schema definitions for a new feature and conversion
of existing records are different work. Existing-record conversion, backfill,
legacy upgrade controllers and upgrade rehearsals are dropped from required
#391 delivery; their existing checkpoints remain saved, not deleted.

The current #401 draft copy/routine-reuse increment requires no additional DDL.
Its scoped queries, native coach UI and authenticated integration are locally
verified, including the combined twelve-case browser run. Mixed-schedule review/publication and the native copy → edit → publish path
are now locally verified too. Dated placement/return/access integration is also
locally verified, including proper-role PostgreSQL and native browser journeys;
see `../dated-training-placements-verification.md`. This does not establish full
#391 acceptance or release. Its fresh-schema definitions, generated queries and
affected read/publication integration are feature requirements, not conversion
of existing records. Craft guidance and both automated real-plan replays are now
verified in `../coach-planning-acceptance.md`; coach-observed usability, actual
screen-reader use and natural midnight return remain untested. The pending owner
question asks whether to complete or explicitly exclude those checks.
Temporary placements return to the previous group; exact season dates remain
unknown and configurable. No destructive database operation is needed or
authorized for this local increment. GitHub and release actions remain separate.

## Disposable acceptance data: latest owner clarification

On 10 November 2026 the owner explicitly said: **“you do not need to
preserve any data!”** In the current local acceptance exercise, fixtures are
disposable. Historical-data preservation, conversion, backfill and upgrade
compatibility are not delivery requirements. Stop constructing historical
sentinels or treating their preservation checks as acceptance gates. Existing
work remains saved; authentication, privacy and enforced application protections
remain in place. No unrelated production operation is implied or needed.

Required work is the fresh feature schema, its query/UI/publication integration,
and proportionate acceptance of the two weekly plans, craft-specific rhythm
guidance and temporary group return. Overlapping rhythm ranges are intentional;
the prescribed workout content is shared while the applicable kayak/canoe
guidance differs. Both plans may replay in the synthetic week 16–22 November
2026, preserving their original 9–15 November reference dates and weekdays in
the evidence. Synthetic season dates are test configuration, not approved club
boundaries. Actual season dates remain unknown and configurable. Temporary
placements return to the exact previous group when they end.
