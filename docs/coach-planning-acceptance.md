# Coach weekly planning — local acceptance

The complete automated two-plan replay passed on 10 November 2026: four native
Chromium cases in **413.083 seconds**, one worker, zero retries, no skipped or
unexpected cases. This is local source acceptance evidence; it does not establish
human coach observation, screen-reader use, a natural midnight rollover or a
production release.

## Actual results

- Clean native setup created both groups, an explicitly synthetic season and
  classification, six athlete participation/sport/craft selections and placements,
  three rhythm profiles and all 27 approved literal zones.
- Both supplied reference plans were entered, reviewed and deliberately published:
  16 + 17 = **33 entries**, producing **99 initial prescriptions**. All source
  lines, weekday positions and explicit distance targets remain; start-only
  workouts keep unknown ends and rest entries retain their day bands. No shorthand
  parser or inferred effort duration, recovery or selected load was introduced.
- Common work stays shared. Kayak, canoe and dual-craft recipients received
  explicitly chosen permitted guidance, including the two distinct canoe tables.
  Intentional overlaps and symbolic/one-sided values remain literal.
- A copied week remained unpublished; editing one copied title left source work,
  schedules and identities unchanged.
- A current-day temporary move withdrew access to 16 old future instructions.
  Destination instructions required explicit publication. The reviewed return
  restores the exact previous group on the day after the temporary inclusive end;
  explicit return publication created 16 changed instructions, with 32 unchanged.
  The delivery lead recomputed all 16 equal-body/session/membership pairs with
  distinct placement identities. Old withdrawn instructions did not revive.
- The current guardian saw their own dependent. Unrelated, suspended and revoked
  controls were denied the new private material. Preview and protected reads left
  the captured business state unchanged.
- Native keyboard/focus, 320px layout, forced colours and genuine Chrome 200% zoom
  completed. Six axe scans found **zero violations**. Screenshots exposed and
  verified a narrow-screen action wrapping fix.

Reference dates are **9–15 November 2026**; approved synthetic replay dates are
**16–22 November 2026**. Actual club season boundaries remain unknown and
configurable. Test season dates are not approved club dates. The current-day
move and future session-date return reads are not a naturally observed midnight
transition.

## Traceability

| Stories | Existing focused verification plus this complete replay |
| --- | --- |
| #392/#393 | [Group and roster verification](training-groups-verification.md); clean groups and all six placements in this run |
| #394/#395 | [Seasons](seasons-verification.md), [setup links](setup-links-verification.md); actual explicit setup in this run |
| #396/#397 | [Dated placements](dated-training-placements-verification.md); current withdrawal and explicit destination/return publication in this run |
| #398 | [Week workspace](week-workspace-verification.md); both seven-day plan shapes and readable mobile actions |
| #399/#400 | [Full creation](full-workout-creation-verification.md), [saved editing](saved-workout-editing-verification.md); all 33 literal entries |
| #401 | [Contextual reuse](contextual-workout-reuse-verification.md); actual copy and one-title edit |
| #402/#403 | [Rhythm verification](training-rhythm-verification.md), [recipient publication](recipient-publication-verification.md); all recipient-specific previews and publications |
| #404 | This complete automated replay; remaining human evidence below |

All Go tests and vet pass on the integrated product source. Focused proper-role
PostgreSQL evidence includes six rhythm data cases, three application cases, the
weekly-load scope regression, and actual modern store create/withdraw/replacement
on START_ONLY with BAND/DAY siblings. Bounded independent security review found
no material finding in the new reference and modern variation paths. Prior
unchanged checks are reused rather than repeated.

The actual browser runtime manifest covers 197 verified files, SHA256
`542b50469babf954837709258885dc9f798457d4336744c1993b3126f50830e7`.
The executed binary SHA256 is
`c88e99de714ca05ad157bea6a0924c73b7fd08d94e8a41f975ffa4ed5b2a90ca`.
The delivery lead independently checked runtime/test identities, the 33-entry
map, stored publications and return pairs, all axe results and exact cleanup.
Private evidence is retained locally; it is not uploaded with public source.

## Private input and reproducibility

The public `e2e/real-plan-input.json` is a harmless discovery placeholder. Full
coach instructions and screenshots remain outside the public repository. The
opt-in runner requires `--real-plan-input /private/path/approved-input.json` in
addition to a fresh evidence directory, unique fixture name, unused loopback
ports and a matching frozen runtime manifest; run
`python3 e2e/run-real-plan-acceptance.py --help` for arguments.

The input must be outside the checkout and contain approved nonempty plan data.
The runner records its digest, copies it into the private read-only browser
assembly and checks those exact bytes. The external-input packaging change was
verified separately for syntax, early refusal and byte-identical input/spec
assembly against the passing run; it is not described as another app execution.
Ordinary test discovery succeeds with the public placeholder.

All test databases were newly owned disposable fixtures. The app stopped and
exact owned containers/volumes were independently confirmed absent with the
Docker daemon reachable. No existing records were converted or backfilled.
Historical preservation exercises and legacy upgrade compatibility are outside
the owner's delivery scope; authentication, privacy and enforced protections
remain. See [owner decisions](planning/391-owner-decisions.md).

## Owner acceptance and follow-up coverage

**Decision recorded 10 November 2026:** Ricardo, the product owner, replied
“Agree” via Telegram and accepted the locally implemented #391 increment and
its missing **human coach-observation** and **screen-reader testing** evidence.
The accepted implementation candidate is
`50c1f5a8624231bc71310e6a8cb72f8c4c5dcd23` on `feat/391-coach-planning`, with the
197-file automated runtime and binary identities recorded above. The passing
automated evidence is unchanged.

Rationale: deliver the already verified useful increment without indefinite
acceptance delay. Mitigation: retain the **NOT RUN** labels and track both as
follow-up coverage. This is an accepted coverage gap, not a test pass.

| Follow-up coverage | Actual status | Disposition |
| --- | --- | --- |
| Human coach-observed usability | **NOT RUN** | Accepted by Ricardo for this increment; follow-up remains open |
| Actual screen-reader testing | **NOT RUN** | Accepted by Ricardo for this increment; follow-up remains open |

This resolves the pending question for those two checks; do not request their
acceptance again.

## Natural midnight return: separate, not waived

Ricardo explicitly did **not** waive this check. Its setup attempts have not yet reached the boundary: one exposed a test
navigation race, and another incorrectly treated the expected future scheduled
return as current membership. Both failures were retained; the harness now
distinguishes scheduled and effective roster states. The fresh fixture's
**before-boundary phase passed**: B is effective now and exact previous group A
is scheduled for 11 November, on ordinary roster URLs without date overrides.
The live application is waiting for the actual **11 November 2026, 00:00
Europe/Lisbon (00:00 UTC)** boundary, with unchanged PID/start identity and binary
checks every 30 seconds. Its separate binary SHA256 is
`378edab20a9e0d21e22d6ac74868f9068da003641ed52702038e11ad4468265a`;
product source matches the same 197-file runtime above. The **natural boundary
result remains PENDING**, not PASS.
Current-day withdrawal and future session-date return evidence above remain
separate from this natural-clock check. The live state and eventual result are
tracked independently; no missing clock evidence is implied accepted.

Acceptance of the two follow-up gaps does not authorize GitHub publication,
merge, production deployment, database reset or unrelated live operations.
