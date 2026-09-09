# Privacy relational execution map

This is the reviewed #245 map for the schema at `202609090001_privacy_relational_erasure`. The older issue inventory counted 82 tables and 65 direct references. Immediately before this migration the baseline contained 92 public tables, 69 textual `REFERENCES users(id)` occurrences, 85 actual foreign-key constraints targeting `users(id)`, and 147 `ON DELETE RESTRICT` constraints. The authoritative column-level list is `relationalUserReferenceInventory`; its PostgreSQL integration test fails when the schema drifts.

| Treatment | Relational scope | Execution rule |
|---|---|---|
| Delete authentication/access | sessions, verification/reset tokens, platform assignments | Delete before the identity tombstone; account start remains the normal early cut-off. |
| Delete subject content | announcement deliveries, event responses, suggestions, profiles, training results and prescriptions, synced activities | Delete only rows whose subject FK matches the request subject. Child rows are removed or detached first inside one checkpoint transaction. |
| Anonymise reporter | repair reporter references | Null the reporter while retaining the equipment-safety record. |
| Pseudonymise membership history | past membership rows and their surviving relational links | Delete athlete-identifying prescriptions first, revoke current/future participation, retain historical membership IDs, replace the live user FK with a purpose-scoped random principal, and keep no subject/execution reverse map. While the job is pending or leased, database guards reject new memberships and prescriptions for the subject. |
| Audit pseudonymisation groundwork | eight pure audit/event families | Companion principal references, immutable-trigger exceptions, recursive text/JSON scrubbing and pseudonymous read labels exist for testable groundwork. The operation remains outside the production allowlist until every intended provenance reference is covered. |
| Restrict | consent, privacy-control evidence, and approved identity/profile fields | Require a durable plan-derived anchor, then move only allowlisted fields into executor-only quarantine. The web role has no table privilege. |
| Keep opaque actor | author/approver/audit actor references | Preserve the UUID for accountability; `IDENTITY_CLEAR` removes the UUID's ability to resolve to name, login, birth date, contact, credential, or active account. |
| Delete relationship | `users.guardian_id` | Execution blocks until dependant resolution leaves no subject-owned guardian relationship. |

Indirect identifier canaries cover prescription/activity/variation JSON, privacy snapshots, audit JSON, and subject-owned notes. Rows in the subject-owned activity, prescription, variation, training-result, profile, and suggestion categories are deleted rather than text-scrubbed. Membership history uses a separate purpose-scoped principal and preserves downstream historical identity by membership ID without retaining a principal-to-user map. Audit groundwork scrubs matching values and object keys and renders a constant removed-user label without exposing the principal UUID. Immutable training prescriptions can be deleted only by the lease-fenced security-definer operation; normal application update/delete remains rejected.

Execution order is the frozen plan-entry order followed by checkpoint position. A job cannot be claimed before earlier entries succeed, and a checkpoint cannot execute before earlier checkpoints succeed. Mutation, the bounded affected-row digest, and checkpoint success commit atomically. Retry returns the prior success without reapplying the mutation.

Retention and external boundaries remain fail closed:

- `RESTRICT`/`EXPIRE` checkpoints require an immutable, source-backed anchor whose code, dates, and retained fields match the frozen plan. This slice exposes no caller-supplied anchor routine; retained operations therefore remain unsupported until a real lifecycle event can populate the anchor safely.
- audit-actor anonymisation remains production-disabled because its current eight-table groundwork does not yet cover every provenance FK. Object-version deletion, provider notification, backup tombstone replay, log expiry, privacy-case restriction, session expiry, outbox expiry, and consent restriction/expiry also remain unsupported and raise `privacy_relational_operation_unsupported` if incorrectly enabled.
- production capabilities and fulfilment activation remain disabled; #246/#247/#248 own cross-store execution, evidence verification, completion, and activation.

Rollback is compensating, not destructive: deploy the prior application while leaving the additive columns/tables/functions in place, disable privacy activation/capabilities, and resume only with a compatible worker. A committed tombstone or deletion is intentionally irreversible and must be restored only through the separately governed backup/tombstone replay process.
