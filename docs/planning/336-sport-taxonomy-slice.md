# #336 sporting taxonomy and legacy consumer inventory

Reviewed against `64d2c2f` on 9 November 2026. This supersedes the older
slice-only inventory, which predated the integrated staff UI and crew migration.
The original inventory is source analysis and isolated regression evidence, not production acceptance. The local #339 announcement follow-up below updates only that consumer.

## Three independent axes

- **Participation:** dated `user_memberships` points to programme, season, optional
  escalão and team. Current participation and explicit staff/guardian grants govern
  programme access; a sporting selection does not confer a role or staff authority.
- **Sporting modality:** `sporting_modalities` contains `CANOEING`, `KAYAK_POLO`
  and `SUP`, with pt-PT labels; `person_sporting_modalities` stores the person's set.
- **Canoagem craft:** `canoe_craft_classes` contains K1/K2/K4/C1/C2/C4;
  `person_canoe_craft_classes` requires that person's `CANOEING` parent. The audited
  service removes craft selections when Canoagem is removed. The shared Polo-team
  uniqueness guard constrains season/programme teams, not sport-selection authority.

`SportAssignmentService.ReplaceForClassification` is the editable staff path;
`Replace` remains the generic scoped correction path. They check active actors and
subjects, current scope and fixed code vocabularies, serialize per subject, and
append per-change actor/reason/grant audit. The staff forms retain stale-set and
preview/confirmation guards. No new taxonomy, permissions or schema are introduced
by this consumer follow-up.

## Inventory method and coverage

Search source SQL and schema/migrations for `modalities`, `membership_modalities`,
`modality_id` and `craft_modality_id`; trace generated query names into handwritten
Go, templ views, CLI and fixtures. Generated sqlc/templ copies are projections of
those sources, not independent consumers. This inventory covers all matching
production query families, handwritten runtime call sites, retained migration and
privacy-history references, and the shared SQL fixture. Historical migrations and
archived predecessor fixture dumps describe their original schema and are not
current product writers.

| Consumer | Current reads/writes and contract | Disposition |
|---|---|---|
| Legacy catalog and membership link | `queries/memberships.sql`: `GetModalityByCode`, `AddMembershipModality`, `ListModalitiesForMembership` read/write legacy UUIDs. No handwritten production Go or CLI callers; DB integration tests use them. `membership_modalities.modality_id` still references `modalities`. | Compatibility-only APIs and retained links. Do not expose as new sporting classification or synchronize them from person corrections. |
| Ordinary training session authoring/edit | `Training.authoring` and `renderSessionEdit` use `ListAnnouncementModalities`; `CreateSession`/`UpdateSession` persist optional `training_sessions.modality_id`. `ui/pages/training.templ` labels this legacy selector “Modalidade (opcional)”. `CreateTrainingSession`, `GetTrainingSessionForEdit`, `UpdateTrainingSession`, `CancelTrainingSession` carry the legacy FK. | Deliberate stored session context, not top-level sporting choices. No FK repointing. Generic legacy label must not be interpreted as the new three-sport catalog. |
| Ordinary training reads | `ListTrainingSessionsForAthlete`, `ListUpcomingTrainingSessionsForDashboard`, `ListTrainingPlansForAuthoring` join `modalities` for a descriptive `modality_name`. Training and dashboard handlers display it; access uses programme/team participation and independent grants. | Keep historical/session labels and UUIDs stable after person corrections. A session label is not a sporting permission. |
| Structured session creation, copying and routines (#28) | `CreateStructuredTrainingSession` inserts no `modality_id` and returns the complete row, including its nullable legacy FK. Segments use the separate WATER/GYM/RUN/BIKE/ERGOMETER/MOBILITY/GAMES enum; water profiles use KAYAK/CANOE. Routine/copy/snapshot functions serialize structured segments and blocks. | These enums describe workout structure, not old modality UUIDs or person sports. Do not conflate them or rewrite copies/publications during a classification correction. |
| Announcements catalog/authoring | `ListAnnouncementModalities` returns legacy UUIDs/names. Admin authoring in `Announcements.renderIndex` and `validate` creates polymorphic `announcement_targets` with type `MODALITY`; coach authorization denies modality/category targets. UI copy uses generic “modalidade”. | Retained target identity; new-person sport codes are not accepted as target UUIDs. No automatic target migration. |
| Current announcement eligibility | `ListVisibleAnnouncements`, `CountUnreadVisibleAnnouncements`, `GetVisibleAnnouncement` map retained `MODALITY` UUIDs through legacy catalog codes to same-subject current person crafts (K1/K2/K4/C1/C2/C4) or SUP selections, plus current dated participation. | **Local #339 announcement cutover implemented:** list, bell/panel/count and detail use corrected selections; current verified guardians and active/non-erased subjects are rechecked. Alternative-target OR, guardian extra restriction, author permissions and historical identities remain. See follow-up evidence below; unreleased. |
| WhatsApp groups | `ListWhatsAppGroupsForUserProgramme` matches legacy `MODALITY` targets through `membership_modalities`; `Dashboard.groups` calls it. Target rows are curated operational data, not a Go modality-ID writer; see `docs/dashboard-data-provisioning.md`. | **Outstanding #339 cutover gap** for modality-target current eligibility. Keep group/target identities; do not infer new membership or staff authority from a sport selection. |
| Competition documents, stored context | `CreateCompetitionDocument` persists optional `competition_documents.modality_id`; legacy FK has RESTRICT deletion. `ListCompetitionDocumentsForAthlete` displays the old catalog name. `ListCompetitionDocumentsForEvent` uses event ID; the event authoring handler writes an event-bound document with no modality. `validCompetitionDocumentInput` has no runtime caller. | Preserve stored document/event identity and descriptive context. No retargeting historical records. |
| Competition document eligibility | `Events.renderIndex`/its form data path calls `ListCompetitionDocumentsForAthlete`. Its modality branch matches current dated membership plus `membership_modalities`, within document programme/team scope. Its event branch uses event audiences. | **Outstanding #339 cutover gap** for modality-target current eligibility; this query is live, not compatibility-only. |
| Temporary crews (#162/#337) | `ListStructuredCrewModalities` now reads **`canoe_craft_classes` codes C2/K2/K4**. `structuredCrewModalities` submits those codes; `structuredCrewSize` and the transaction store recheck catalog/capacity. `CreateTrainingVariationGroup` writes `craft_code`; `ListManagedTrainingVariationGroups` reads it. | Already adapted. The old `craft_modality_id` column was converted then dropped by `202609300002_temporary_canoe_crews.sql`; only historical migrations still mention it. No runtime UUID crew selector remains. K1/C1/C4 remain valid personal craft choices, not supported temporary crew types. |
| Published training | `training_plan_publications` and `training_prescriptions` retain immutable revisions, person/membership/session IDs, snapshots and stored hashes. Structured publication/read queries consume those records; membership/grant checks remain separate. | Never rewrite a published prescription or its embedded legacy context following a person-sport correction. |
| Sealed membership/privacy history | `privacy_membership_history_compute` includes legacy modality IDs in the canonical membership-history digest. `membership_modalities_sealed_history` guards sealed links. The retained synthetic restore fixture selects a legacy modality and seeds a link. | Retained compatibility/history integrity, not an active rights-execution path. #318 automation remains retired; manual privacy/restore work has separate ownership. |
| Shared UI fixture | `scripts/ui-review-seed.sql` seeds old K1 membership/session UUIDs intentionally. Archived DB fixtures and earlier migrations also retain their original identities. | Synthetic legacy test data; no new-person sport writer. No fixture-ID reassignment to make the new taxonomy appear compatible. |

## Targeted regression and executed evidence

`internal/db/sport_consumers_integration_test.go` adds
`TestSportCorrectionsPreserveLegacyTrainingConsumersAndAuthority`.
It uses the real correction service, `ListPersonSportingModalities`,
`ListTrainingSessionsForAthlete`, `GetTrainingSessionForEdit` and
`ListStructuredCrewModalities` against PostgreSQL. It exercises all three sports,
then C2/K2/K4 personal crafts, then removal of Canoagem while SUP remains. The
new read models must actually change, including craft cascade removal, while:

- the same Leisure participation and legacy K1 membership link stay byte-for-byte
  equal at the JSONB row representation;
- the old K1 training session still displays “Caiaque individual” and retains its
  prescription link and legacy UUID;
- the crew selector continues to offer C2/K2/K4 codes, not legacy UUIDs;
- stored document, session, publication and prescription rows—including IDs,
  snapshot and stored hash string—stay equal;
- member roles/grants stay equal and the athlete still cannot authorize a correction.

The publication/prescription are synthetic persisted fixtures. The snapshot hash
is an opaque fixture value; equality does not certify that value as a genuine
publisher-generated checksum or test the publication workflow. The test does not
assert stale legacy audience eligibility as correct new behavior.

Existing complementary coverage remains: `TestSportTaxonomyIndependentOfParticipationAndLegacyReferences`
(invalid taxonomy/parent combinations, fixed labels, shared Polo team),
`TestSportTaxonomyScopedAssignmentDoesNotGrantAuthority`,
`TestSportAssignmentCorrectionScopedAndAudited`,
`TestTemporaryCanoeCrewsOverlapWithoutParticipationWrites`, and
`TestPostgresTemporaryCrewsRequireDatedScopedComposition`. Together they cover
cases the consumer regression deliberately does not duplicate.

Run against a fresh disposable database from the baseline and migration ledger:

```sh
TEST_DATABASE_URL='<isolated PostgreSQL URL>' go test -tags integration ./internal/db \
  -run '^TestSportCorrectionsPreserveLegacyTrainingConsumersAndAuthority$' -count=1
```

The regression passed on 9 November 2026 against the separately provisioned
PostgreSQL 16.9/Lisbon fixture for this branch. This is a tests/documentation-only
change: no migration, sqlc regeneration, compensation or production rollback is
required; ordinary test rows live only in the isolated disposable fixture.

## Combined branch verification

The integrated branch also adds
`TestStructuredCrewInputRequiresCraftCodeAndGroupAuthority`: fifteen handler cases
cover administrator/scoped/unscoped actors with C2/K2/K4 codes, a legacy UUID in
`craft_code`, and the obsolete `craft_modality_id` field. Valid submissions retain
the exact group, actor and membership identities; unauthorized or obsolete inputs
return 403 without reaching persistence.

On 9 November 2026, all-package `go test ./...` and `go vet ./...` passed. Six
focused PostgreSQL regressions and the fifteen handler cases passed together,
without skips, on the isolated PostgreSQL 16.9/Lisbon baseline with all 78 ledger
markers. Independent read-only QA found no material blocker. Evidence is under
`/tmp/mycfc-336-consumers-20261109` (local, ephemeral); the fixture was cleaned up.
No production behavior, schema or query changed, so no browser rerun or sqlc
regeneration is represented as new evidence.

```sh
TEST_DATABASE_URL='<isolated PostgreSQL URL>' go test -tags integration \
  ./internal/db ./internal/handlers \
  -run '^Test(SportTaxonomy|SportAssignmentCorrection|SportCorrections|TemporaryCanoeCrews|PostgresTemporaryCrews|StructuredCrewInput)' \
  -count=1 -v
```

## Remaining boundaries

This completes the reviewed consumer inventory and adds a current-source targeted
compatibility regression. The local announcement follow-up below implements only that #339 consumer. It does **not** implement the WhatsApp/
modality-document audience cutover, approve retention periods or former-scope
history access, prove manual erasure/restored-copy postconditions, or authorize
GitHub closure, release, live migration or deployment. Those gaps must remain
visible rather than be called solved by the inventory or historical equality test.

## Local #339 announcement-only follow-up (11 November 2026)

Production checkpoint `b5ed711638cc978d334b971b11887d4899fb4108` changes only
`internal/db/queries/announcements.sql` and its pinned sqlc projection. Existing
handlers already share list/count/detail queries; the authoring catalog and stored
UUID targets remain unchanged. No migration, historical-row update or permission
expansion is introduced. Eligibility changes on the next request/navigation;
already-rendered content and the page-lifetime bell cache do not receive push updates.

`internal/db/announcement_sporting_audience_integration_test.go` at `16cec6d`
uses real scoped correction service writes and guardian changes, all seven mappings,
unknown target controls, stale legacy links, same-subject guardian positives/negatives,
revoked/inactive children, no/future/ended and inclusive today participation,
multiple dependents, OR targeting, deterministic paging/deduplication, lifecycle
exclusions and immutable target/receipt/history snapshots. The existing legacy-consumer
regression separately covers retained training/document/publication identities.
All-package Go tests/vet, generation parity and 28 focused tagged PostgreSQL/handler
tests passed at the earlier checkpoint; the test-only strengthening passed its focused
PostgreSQL rerun without skips and tagged vet. Independent QA cleared the backend;
independent security review found no material production-diff finding.

The six new announcement browser files and opt-in disposable runner are described in
`e2e/announcement-audience-339.md`. Final revised evidence at
`/tmp/announcement-339-browser-evidence-qa-fixes` records four pinned Chromium cases,
zero failures/skips/retries, actual UI corrections/cascade, exact six-plus-two list
pages, panel/unread/detail parity, retained-cookie guardian revocation, historical
receipt equality, keyboard focus and 320px serious/critical axe checks. It builds the
exact production checkpoint externally and verifies owned container/volume/port
cleanup before recording success. Independent final QA verified the browser evidence and closed the pagination/cleanup findings with no material blocker for this bounded slice. Logs under `/tmp`
are local and ephemeral. Four safely attributable earlier leaked volumes were removed;
possible volumes from attempts -1 through -3 were not safely attributable and were
left untouched, with no global prune. This is a local housekeeping limitation.

Full repository/browser/Terraform verification, manual screen-reader/real zoom,
production operational guardian activation and release readiness are not established.
No GitHub write, main integration, merge, publication, live migration or deployment
has been performed by this slice.
