# Coach planning: confirmed owner decisions

These decisions guide #391 and its dependent stories. They record product
authority, not implementation, acceptance or release completion.

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

Recipient publication review, full structured/mixed workout creation, and
identity-preserving editing/cancellation are implemented and locally verified.
The next bounded increment is contextual copying and routine reuse through the
existing models. It needs scoped queries, authenticated handler/UI integration,
review/replay and lifecycle checks, and proportionate real PostgreSQL/browser
proof on newly owned disposable fixtures. No new schema definition is currently
required; retain the implemented fresh-schema integrity constraints rather than
adding existing-record conversion or upgrade rehearsals.
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
The temporary-return workflow is not implemented by this decision record.

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
are now locally verified too. Dated placement/return/access integration is next;
this does not establish full #391 acceptance or release.
Later dated placements still need fresh-schema placement/return/access-withdrawal
definitions, generated queries and all affected read/publication integration.
Those are feature requirements, not a request to convert existing records.
Temporary placements return to the previous group; exact season dates remain
unknown and configurable. No destructive database operation is needed or
authorized for this local increment. GitHub and release actions remain separate.
