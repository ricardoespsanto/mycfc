Child of #21. Supersedes the former result-import discovery scope following the product decision on 13 September 2026.

## Outcome

People authorized to view a MyCFC competition event can open its official FPC results directly. Administrators curate the link; FPC remains the source of the results.

## Approved scope

- One optional official FPC results link per existing `COMPETITION` event.
- Administrators can add, replace and remove the link, including after the event has started or finished. Use a dedicated results-link action; do not reopen editing of event dates, attendance or audiences.
- Display “Resultados oficiais na FPC” on the authorized event detail, clearly identified as an external link.
- Reuse current event visibility and administrator authorization on reads and writes. No new coach permissions or access based on historical membership/guardianship.
- Preserve existing competition documents and completed #144 numeric-only athlete history links.
- Link only: no result imports, scraping, previews, proxying, automatic link checks, athlete matching, stored race/crew results or ranking snapshots.

## URL and integrity contract

- Accept bounded absolute HTTPS URLs on exact approved FPC hosts (`fpcanoagem.pt` and `www.fpcanoagem.pt` initially). Other hosts/subdomains require explicit assessment; a suffix match is insufficient.
- Reject credentials, unexpected ports, control characters and malformed URLs; do not accept lookalike domains or script URLs. Avoid user-specific token/credential links.
- Store link metadata only, with actor/time and optimistic concurrency for replacement/removal. No remote fetch on save or view; administrator review establishes that the destination is the relevant official result page or document.
- FPC availability or subsequent changes are external: the UI must not claim that MyCFC continuously verifies availability or synchronizes corrections.

## UX and acceptance criteria

- [ ] An administrator can add, replace and remove a valid FPC results link on an existing competition, including after it ends.
- [ ] Non-administrators cannot mutate the link through the UI or a direct request.
- [ ] Only viewers already authorized for the event can see its link; unrelated users gain no event access.
- [ ] An event with a results link cannot change from `COMPETITION` to `GENERAL` until an administrator removes the link.
- [ ] General events reject results links. Existing cancellation/read-access semantics remain in force.
- [ ] A saved link appears as “Resultados oficiais na FPC”; absent/removed links produce no empty member-facing control.
- [ ] Malformed/non-FPC URLs are rejected with a pt-PT field error and the administrator's input preserved.
- [ ] Stale concurrent writes cannot silently replace a newer link.
- [ ] Existing official document links and #144 athlete history links remain unchanged.
- [ ] No FPC request or result ingestion occurs on save or event view.
- [ ] Keyboard, mobile and accessible validation states are covered.

## Implementation and verification

Reuse the existing event detail and authorization flow. Keep link management separate from future-event editing. Choose the smallest compatible persistence extension after technical review; any new live schema requires baseline plus forward migration and generated queries. Audit/provenance must follow existing privacy conventions and appear in the erasure inventory where new user references are introduced.

Verify URL validation, authorization, post-event updates, removal, concurrency, event-type constraints and event visibility with focused unit/integration tests, plus the relevant browser/axe journey. Source implementation, merge and production deployment remain distinct milestones. No estimate carried over from the former discovery issue.

## Deferred scope

#141 stored results/imports, #142 MyCFC-stored competition history and #143 ranking snapshots are deferred. Their earlier acceptance criteria are not fulfilled by this linking feature. Reopening them requires a fresh source/data/retention contract. The former #140 discovery questions and fixtures are not prerequisites for this outbound-link slice.

## Source delivery evidence — 13 September 2026

Implemented on `codex/140-fpc-results-links`. Independent QA and security review found no remaining material findings. Generation, full Go vet/unit tests, deployment checks and Terraform checks passed through `make verify`; its host browser step was environment-blocked (output ownership, then missing Chromium). The replacement pinned-container `make test-e2e` passed: 38 passed, 31 skipped, including the new results workflow. Final fresh-database focused integration tests passed for results lifecycle, authorization, migration, both query lock orders and privacy reference inventory. Desktop and 320px screenshots were inspected.

The user approved an all-events archive extension: `/events?view=past` shows completed events newest first with pagination and current access rules. Ongoing/future events remain in the default view. Event detail and guardian subject-switching preserve archive return context. No additional schema change is required. No merge, production migration or deployment performed. Applying the forward migration follows the existing release boundary and invalidates prior privacy/guardian activation evidence.
