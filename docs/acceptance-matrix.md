# Acceptance evidence matrix

This is the phase-10 status for the implemented application slice. A row is only marked covered where an executable test exists; manual checks and unavailable environments remain explicit.

| Workflow | Unit evidence | Integration evidence | Browser evidence | Status / gap |
|---|---|---|---|---|
| Adult registration, login, session, consent validation | `internal/handlers/registration_test.go`, `login_test.go` | `internal/handlers/integration_test.go:TestPostgresRegistrationStorePersistsConsentsAtomically` | `e2e/auth.spec.mjs` registration and no-JavaScript login | Covered |
| Guardian dependants and minor credentials | `guardian_test.go`, `members_test.go`, `auth_test.go` | `TestPostgresGuardianDependentStorePersistsResponsibilityAndEnforcesLimit`, `TestMinorCredentialRequiresCurrentGuardianAndWritesAudit` | `e2e/auth.spec.mjs` dependant creation without JavaScript | Covered; minor credential issuance/recovery has no browser flow |
| Role workspaces and access control | `auth_test.go`, `dashboard_test.go` | `internal/db/integration_test.go:TestMembershipsResolveActiveSportStructureAndRejectMismatches` | Registration dashboard redirect and administrator fleet in `e2e/auth.spec.mjs` | Covered at unit/integration; coach/moderator browser journeys remain absent |
| Member management | `members_test.go` | None | Administrator creates an adult and assigns a competition membership in `e2e/auth.spec.mjs` | Covered for adult creation and membership assignment; dependant credentials and deactivation lack browser evidence |
| Events | `events_test.go` | `TestListEventsForTodayRespectsMembershipCoachGrantAndAdminVisibility` | Administrator authoring, RSVP, capacity/waitlist, confirmation, and check-in in `e2e/auth.spec.mjs` | Covered for administrator and adult member flows; coach and guardian browser journeys remain absent |
| Announcements and official documents | `announcements_test.go` | None | Targeted publication, first-read delivery, and expiry in `e2e/auth.spec.mjs` | Covered for administrator and competition-athlete lifecycle; coach and official-document browser journeys remain absent |
| News | `news_test.go`, `dashboard_test.go` | None | Administrator draft/publish/expire and leisure-workspace visibility in `e2e/auth.spec.mjs` | Covered for past scheduled publication; future-time automatic publication remains untested |
| Repair reporting and status transitions | `repair_test.go`, `dashboard_test.go` | `TestCreateRepairRequestEnforcesIdempotencyKeyDuringConcurrentRetries` | `e2e/auth.spec.mjs` photo upload and same-key retry | Covered for hostile invalid upload, cross-user retry, database cleanup, concurrent duplicate creation and stale status conflict; administrator resolution lacks browser evidence |
| Maintenance scheduling and completion | `dashboard_test.go` | `TestScheduleMaintenanceTaskUpdatesOnlyDueEquipment` | `e2e/auth.spec.mjs` administrator validation, keyboard scheduling and completion | Covered |
| Training | `training_test.go` | None | None | Unit scope, athlete outcome, and document validation only; plan/session/outcome persistence needs integration/browser evidence |
| Validation and error responses | Handler tests including `login_test.go`, `registration_test.go`, `repair_test.go`, `dashboard_test.go` | Transaction rollback in registration integration test | Registration, maintenance and repair validation paths in `e2e/auth.spec.mjs` | Covered for implemented forms; error-summary focus still needs manual verification |
| Responsive, keyboard and accessibility | Component and handler rendering tests | None | Axe, keyboard registration/logout/maintenance, 320px overflow and 200% zoom fleet coverage in `e2e/auth.spec.mjs` | Automated baseline covered; screen-reader, contrast, all focus order, calendar fallback and all routes remain manual per `docs/accessibility-manual-check.md` |

## Commands

| Evidence layer | Command | Environment |
|---|---|---|
| Unit | `make test` | Local Go toolchain |
| Database and object-storage integration | `make test-integration` | Docker, `.env`, PostgreSQL and MinIO |
| Browser and axe | `make test-e2e` | Docker, `.env`; starts the pinned Playwright container and seeded local stack |

`make verify-foundation` remains the deterministic foundation gate. `make verify` is intentionally not an acceptance gate until the documented production Terraform and remaining acceptance scope are implemented.
