# Privacy post-matrix acceptance audit

Date: `2026-09-08`. Status: reconciled audit; #243 plan-contract findings are implemented on the #110 delivery branch and source tagging as `v1.14.0` was approved. Merge, activation, deployment and other production actions remain separately gated.

## Outcome

The approved internal matrix choices are coherent enough to define implementation. #243 now gives #111 a versioned, immutable machine-readable handoff; production adoption still fails closed for unresolved external categories/provider facts, and no #111 execution behavior exists yet.

The baseline currently contains 82 tables, 65 direct references to `users(id)` and 78 `ON DELETE RESTRICT` relationships. Direct references are not the complete inventory: personal or linkable values also occur in JSON, arrays, generic UUIDs, encrypted payloads, session blobs, S3 metadata, provider identifiers, logs and backups.

## Blocking findings

| Priority | Finding | Required outcome |
|---|---|---|
| Resolved locally by #243 | Arbitrary catalogue actions and unsupported compatibility versions previously passed syntactic validation. | A closed execution-profile registry now derives disposition/action/operation codes; unknown executor/schema/profile/field combinations fail import, intake, decision and activation validation. |
| Resolved locally by #243 | A `RETAIN` decision previously recorded only a ground. | The server now freezes every selected category into an immutable plan with exact retained-field codes, owner role, legal ground, decision-anchored execution deadline, allowlisted retention anchor/calendar-day offsets, exact review/expiry timestamps only for an already-resolved anchor, `BLOCK` fallback and SHA-256 digest. |
| Partially resolved by #243; #248 remains | Activation trusted a caller-supplied executor-ready boolean and stored no validated evidence. | The CLI assertion was removed and enablement now fails closed unconditionally. #248 must bind future activation to installed capabilities and an immutable evidence digest. |
| P0 | The case lifecycle ends at `AWAITING_EXECUTION`/`PARTIALLY_APPROVED`; there is no durable execution, category work set, `PROCESSING`, recoverable failure or `COMPLETED` state. | Add idempotent execution and job state. Accept every required work item transactionally before any account cutoff. |
| P0 | `users` cannot currently become the approved opaque non-authenticating principal: its name, birth date and adult identity shape are mandatory, while roughly 50 operational/audit foreign keys depend on it. | Add an `erased_at` tombstone identity shape: inactive, credential-free, guardian-free, non-visible, with name/birth/login identity cleared. Retained joins may keep only the opaque UUID. |
| P0 | Versioned S3 deletion uses ordinary `DeleteObject`, which creates a marker rather than deleting all versions. Runtime IAM lacks version-list/delete permissions; repair-object lifecycle keeps non-current versions 90 days, and backup non-current versions do not expire. | Add protected version-aware jobs, least-privilege permissions, absence verification and approved lifecycle rules before completion or activation evidence. |
| P0 | No independent 24-month tombstone ledger or mandatory restore replay exists. | Store the encrypted ledger outside the restored dataset; block traffic, email and providers until replay and verification succeed. |
| Resolved in source by #247; live activation remains gated | General cleanup originally omitted consent evidence and had no consent withdrawal/cessation/expiry anchors or dedicated scheduler. | Migration 010 adds immutable cessation plus exact three-year evidence expiry, retains the independent 12-month IP/UA scrub, extends bounded maintenance, and supplies a separate least-privilege CLI/timer with aggregate backlog evidence. The timer, database membership, secret and infrastructure apply remain inactive pending separate approval. |
| P1 | Object keys are logged in three deletion-error paths, and equipment audit JSON stores object keys. | Remove prohibited log attributes and scrub/migrate JSON according to the matrix without weakening audit integrity. |
| P1 | S3 objects carry raw `uploaded-by-user-id` and request-ID metadata. Equipment photos have no direct owner link in their row, so retained technical objects can remain linkable to an erased uploader. | Inventory or migrate ownership; delete subject-owned objects, or rewrite retained non-personal objects with sanitized metadata and delete every old version. |
| P1 | `training_prescriptions` must be deleted for approved individual erasure, but its trigger rejects every update or delete. Other append-only audit tables also lack a controlled expiry/scrub path. | Add narrowly authorized executor/expiry paths that preserve append-only guarantees for ordinary callers and prove historical JSON is sanitized. |
| P1 | Session rows contain gob-encoded `user_id` but have no subject index; privacy authentication throttles store raw `actor:<uuid>` buckets for up to one day. | Add a trustworthy session-subject index or safely decode/purge matching rows, retain credential-version invalidation, and key/HMAC both throttle dimensions. |
| P1 | Provider connection rows contain credential ciphertext, provider user/activity IDs, scopes, cursors, errors and raw JSON. No provider adapter is active, but the schema can hold the data. | Capture disconnect work before clearing local values; remove local credentials even during provider failure; exclude inactive providers from activation until supported. |
| P1 | Retrying a remote disconnect needs a credential after the active connection must stop using it; clearing credentials immediately makes retry impossible. | Move only the required encrypted credential into an executor-only quarantine job, inaccessible to sync/web code, and purge it on success or approved terminal handling. |
| P1 | Privacy email ciphertext has no key identifier, making safe key rotation and delayed completion delivery ambiguous. | Version encrypted job/outbox payloads and define redaction/rotation behavior before production use. |
| P1 | Several approved-policy statements in operational documentation still describe the wider matrix as unapproved. | Reconcile matrix, runbooks, privacy notice, implementation status and GitHub issues to one explicit partial-adoption status. |

## Data coverage contract

| Execution group | Stores and hidden references | Approved treatment |
|---|---|---|
| Identity/authentication | `users`, gob session data, verification/reset tokens, typed outbox, minor credential audit/login ID and authentication throttle buckets | Disable and increment credential version at accepted account-closure start; cancel/delete credentials and tokens; clear tombstoned identity fields; purge matching sessions and expire all rows within 12 hours; pseudonymize minimal audit and throttles. |
| Profile/consent | `member_profiles`, `consent_forms`, profile audit, profile-photo key and S3 versions | Delete profile/health/emergency/photo; preserve only approved consent evidence; remove IP/UA on its own schedule; scrub actor/subject references. |
| Relationships/access | guardian link, platform roles, memberships/modalities, training groups/crews, staff and privacy reviewer grants/audits | Resolve dependants first; revoke active access; delete individual participation; retain only pseudonymous, time-bounded evidence where approved. |
| Events/communications | event responses/check-ins/actors, announcements/deliveries/audits, suggestions | Delete individual response/read/delivery/suggestion data by scope or schedule; preserve content only with pseudonymized authorship; isolate documented incidents. |
| Training/activity | logs, metrics, prescriptions, outcomes, memberships, variation generic `subject_id`, snapshots/patches, connections/jobs/activities/matches and raw/provider JSON | Delete individual prescriptions/outcomes/metrics/provider data; scrub generic IDs and personal JSON; preserve person-free methodology only. |
| Fleet/content history | repairs, maintenance, equipment, equipment JSON audits, albums/audits, competition documents, feature audit | Remove reporter/personal text/photos; sanitize object metadata and JSON; pseudonymize authors while retaining approved technical/content history. |
| Privacy cases | requests, category decisions, policy snapshots, events, dependant resolutions, holds, grants/activation/maintenance events | Working data 90 days and minimal evidence 24 months after actual closure; holds contain exact scope/owner/expiry; actor references become opaque; completed cases are covered by expiry. |
| Infrastructure/external | S3 current/non-current objects, PostgreSQL/Hetzner backups, SES, Cloudflare, GitHub artefacts, application/host logs | Apply approved lifecycle, durable external work, provider register and restore replay; unresolved or unsupported active services block activation. |

## Transaction and completion invariants

1. Lock the case and affected accounts, then validate the frozen plan, dependant resolution, reviewer independence and last-administrator protection.
2. In one database transaction, create a unique execution plus every category/object/provider/recipient job. For account closure only, atomically disable authentication, increment credential version, revoke active roles/grants and cancel credentials/tokens after all mandatory work is durably accepted.
3. Category-only execution must not disable the account unless its frozen plan explicitly requires that restriction.
4. Relational work is deterministic and restartable. A duplicate worker, rollback, crash before/after commit or repeated call converges without re-identifying data.
5. Object/provider jobs use bounded retry and opaque diagnostics. Already absent/disconnected is success.
   Version-deletion and quarantined-provider credentials use executor-only IAM/DB access, not the internet-facing web identity.
6. `COMPLETED` requires all mandatory database, object, provider/recipient and restore-safety controls to succeed. A permanent third-party limitation is recorded as an approved structured outcome rather than silently treated as deletion or left pending forever.
7. The 90-day working and 24-month evidence clocks start only at actual case closure, not reviewer approval.

## Verification contract

- Generate a maximally connected synthetic adult, dependant and actor graph covering every direct/indirect person reference, JSON/array field and all three current object-key families.
- Prove exact delete/anonymize/restrict/expire outcomes and absence of unexpected foreign-key failures.
- Cover two workers, stale versions, rollback, crash boundaries, retry exhaustion, already-absent objects/providers and partial scopes.
- Prove old sessions fail immediately and physically expire by 12 hours; no new authentication succeeds after account-closure start.
- Run version-aware MinIO/S3 tests for current/non-current versions, delete markers, sanitized retained objects and dangling metadata.
- Inspect logs, metrics, audits, job screens and emails for names, email/login IDs, subject/raw case IDs, IP addresses, object keys, provider IDs, credentials, medical/profile values and arbitrary explanations.
- Restore the oldest permitted synthetic backup in isolation, replay the independent ledger, verify all categories and objects, then prove traffic/email/providers remain blocked until the gate passes.
- Run `make verify`, PostgreSQL/MinIO integration, browser/axe approval-to-final-status coverage and the documented restore drill.

## GitHub reconciliation draft

### #108 — outcome epic

Keep the epic open until four independently evidenced outcomes exist: published/versioned legal information; one formally adopted executable matrix version; merged request/review with a frozen execution plan; and completed executor/restore evidence followed by separately approved activation. A disabled implementation or merged code alone does not complete the epic.

### #109 — legal publication and matrix adoption

Record the published legal/navigation work as delivered. Keep the issue open for: formal status/version of the internally approved matrix; exclusion of unresolved categories from production; exact stable category keys/dispositions/periods/exception fields; factual provider register; cross-document consistency; and a mechanical comparison between the adopted document and executable catalogue.

Suggested external-evidence child, if it has a separate non-engineering owner: **Complete provider, recipient, health, federation and insurance evidence**.

### #110 — request/review and executable decision handoff

Replace the outdated lifecycle with the implemented reviewer-capability model and `AWAITING_EXECUTION` boundary. Record 90-day working and 24-month minimal evidence periods, disabled-by-default status and the absence of account cutoff. Add a required follow-up before #111 execution: **Persist an executable per-category decision plan**, including allowlisted action version, exact field codes for restriction, ground, owner, expiry/review, fallback disposition and immutable snapshot/hash.

### #111 — executor integration issue

Keep #111 as the integration and activation-evidence issue. Split implementation into five reviewable children:

1. **Durable execution handoff and lifecycle**
2. **Relational deletion, anonymization and restricted retention**
3. **Versioned objects, providers and recipients**
4. **Tombstone ledger, retention schedules and restore safety**
5. **Completion, operations, user evidence and activation proof**

The parent accepts only when the transaction/completion and verification contracts above pass and all schema, generated code, runbooks, acceptance evidence and policy documents agree.

## Remaining human/external gates

- Formally mark matrix `2026-09-08` as adopted for its approved internal scope while explicitly excluding unresolved external categories, or keep the whole document proposed. Recommended: scoped adoption with fail-closed exclusions.
- Decide whether activation evidence is required for every registered provider or only currently active services capable of holding in-scope data. Recommended: active services block activation; inactive/future providers remain excluded from the production catalogue until evidenced.
- Approve the proposed GitHub issue edits and child-issue creation before any GitHub write.
- Approve implementation of the newly identified executable-plan prerequisite and #111 slices.
- Separately name reviewer accounts, grant authority, approve merge/release, configure production, activate the feature and authorize destructive live executions.
