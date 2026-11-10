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

Recipient publication review and full structured/mixed workout creation are
implemented and locally verified. The next bounded increment is identity-preserving
editing using the existing models and lifecycle rules. Necessary work includes fresh schema integrity definitions, scoped
queries, authenticated handler/UI integration and proportionate real tests.
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
