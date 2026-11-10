# Coach planning: confirmed owner decisions

These decisions guide #391 and its dependent stories. They record product
authority, not implementation, acceptance or release completion.

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
