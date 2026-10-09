# Manually approved classification-audit full deletion

Source-only #334 disposition: delete affected `person_sport_assignment_events` rows in full and clear the complete `age_exception_reason` / `age_exception_by_id` / `age_exception_at` triple. No pseudonym or reason fragment is retained. This is **not account erasure**, a retention policy, or a resurrection of #318 automation. The manual, external approval boundary in [privacy-rights-operations.md](privacy-rights-operations.md#rights-requests) remains mandatory.

## Approval and exact scope

An authorized officer must verify identity and approve the environment, target UUID and exact additional records **outside the database**. Review:

- All audit rows whose subject **or actor** is the target (the operation selects these automatically).
- All membership exceptions whose member **or exception actor** is the target (selected automatically).
- Audit rows linked through the target's coach grants, and identifying third-party references in reasons. Supply reviewed event IDs with their expected subject UUIDs; grant links are not automatically deleted because a shared grant can account for unrelated changes.
- Third-party references in membership reasons. Supply reviewed membership IDs with their expected member UUIDs.

The JSON arrays contain only `{ "id": ..., "subject": "UUID" }`. Event IDs are positive bigint values; membership IDs are UUIDs. A supplied ID bound to the wrong subject, malformed scope, unknown target, or (on the initial call) missing event is rejected before mutation. Reviewed membership IDs must exist, match the supplied member, and have exception provenance or the protected erased-state marker. Repeated identical scope IDs and subject/actor overlap are counted once. Conflicting subjects for an existing ID fail closed. Do not supply speculative IDs, broaden scope on failure, or pretend name-string matching finds all people in text. Exact free-text/grant scope review remains a human prerequisite; the database cannot certify that review.

`replay=false` is the initial operation. Only after external evidence establishes that this exact scope was previously approved/executed may the operator use `replay=true` for an idempotent retry or restore reconciliation. This explicitly permits already-absent event IDs (no durable deletion ledger is created). An absent ID cannot be validated against a deleted row; its authenticity must come from that approved external manifest, not a successful replay. Existing mismatched subjects still fail. The checker likewise permits absent event IDs because absence is its desired postcondition. This bounded API does not prove whole-account erasure.

## Privilege boundary

Migration `202610010001_manual_classification_erasure` and the fresh baseline install a private `classification_erasure` schema. Its functions, fence and erased-state table have no PUBLIC grants. No web, media-cleanup, retention or retired privacy role is granted this capability. There is no HTTP route, worker, timer, activation or automatic restore hook.

The existing trusted **migration owner** owns the schema and SECURITY DEFINER operations; they set `search_path=pg_catalog,public,pg_temp`. Use that existing offline migration capability (or the database administrator) only after separate human approval. Do not grant schema usage/execution, role inheritance, ownership or schema creation to the application/retired roles, and do not put migration credentials in a serving process. Owner/administrator powers are inherently trusted; the operation does not constrain a malicious database owner. Verify the installed owner and ACLs before live use. No new login is provisioned by this slice.

The private `classification_erasure.erased_memberships` table stores `membership_id`, without a separate actor, timestamp, reason, case or legal evidence. This is restricted **linkable personal metadata**, not nonpersonal or anonymous data: its foreign key joins to `user_memberships.user_id` and the retained classification. The membership and marker remain; full deletion covers the approved reasons/events, not this metadata, and does not constitute pseudonymous whole-account erasure. The marker follows the surviving membership lifecycle. Exact retention/disposition must be reviewed against existing retention decisions and separately approved for the manual case before real-user adoption; this source-only draft sets no new retention term and grants no adoption or live-operation approval. It permits an existing category mismatch to survive removal of its provenance without changing membership IDs, dates, team, programme or category. Only the manual function writes this marker. Ordinary writes cannot forge it, reinstate exception provenance, change its classification anchors, or reuse it for a new exception. Existing interval-end/event/training/prescription history guards remain active. The transient fence contains only transaction-local allowed IDs and is empty after success; on failure the entire transaction rolls back. There is no arbitrary GUC bypass, trigger disabling or audit anonymization.

## Execute and verify

Arrange an approved maintenance window; stop/drain classification writers and keep them stopped through the postcheck and any separately approved core-identity work. Use READ COMMITTED (other isolation levels are rejected), a bounded lock timeout and a transaction. Table-level SHARE ROW EXCLUSIVE locks on both audit events and memberships wait for prior writers and block new writes until commit/rollback. The function validates scope, deduplicates the selected rows, deletes/clears atomically, removes its transient fence and runs the same aggregate postcheck before returning.

In a protected, case-specific SQL file, set the psql variables `target_user_id`, `reviewed_event_scope` and `reviewed_membership_scope` to the externally approved identifiers/JSON. Do not include correspondence or reasons. Execute only through the separately approved connection; use `psql -X -v ON_ERROR_STOP=1 --file=<protected-approved-case.sql>` without putting identities or credentials in command-line arguments or logs. Template body:

```sql
BEGIN ISOLATION LEVEL READ COMMITTED;
SET LOCAL lock_timeout = '10s';
SET LOCAL statement_timeout = '60s';
SELECT * FROM classification_erasure.check_remaining(
  :'target_user_id'::uuid, :'reviewed_event_scope'::jsonb,
  :'reviewed_membership_scope'::jsonb);
SELECT * FROM classification_erasure.erase(
  :'target_user_id'::uuid, :'reviewed_event_scope'::jsonb,
  :'reviewed_membership_scope'::jsonb, false);
SELECT * FROM classification_erasure.check_remaining(
  :'target_user_id'::uuid, :'reviewed_event_scope'::jsonb,
  :'reviewed_membership_scope'::jsonb);
-- Both audit_events and exception_reasons must be zero. Otherwise ROLLBACK.
-- shared_definition_approver_links is separate inventory, NOT an erasure failure.
COMMIT;
```

Capture only approved aggregate counts/result in external evidence. A checker reports `audit_events`, `exception_reasons`, and a separate `shared_definition_approver_links` inventory of category approver references. Shared definitions, grants, membership rows, current sporting selections, event responses, training outcomes, publications and prescription snapshots are never erased by this operation. Remaining shared definition-approver links require separate review; do not silently expand this capability to remove them.

**Bounded race guarantee:** locks fence both affected row families until transaction completion, including concurrent audit inserts. After commit, a writer may create fresh provenance; there is deliberately no automatic tombstone ledger or permanent writer fence. External maintenance/approval coordination and any separately approved identity lifecycle control are required. A zero postcheck certifies this exact target and reviewed scope at that transaction boundary, not all future writes or undiscovered free-text references.

## Restore without resurrection

Retain the approved scope manifest and evidence outside MyCFC so it survives backup rollback. Restore backups only into an isolated, **non-serving** database. Before serving, reconcile every applicable approved case with the same manual operation (using `replay=true` only against the authenticated approved scope), then require zero `audit_events` and `exception_reasons` from the checker. Backups/restores also contain surviving memberships and any erased-state markers present at backup time; reconciliation may recreate markers when removing restored exception provenance. Inventory and verify this restricted linkable metadata under the approved retention/disposition review, as well as preserved histories, snapshots, current selections and remaining shared-definition references. Zero affected reasons/events is not a blanket absence claim for personal data. A failure or uncertain scope keeps the restore isolated. Never automatically reactivate retired privacy roles or declare the generic schema/retired-flag restore drill to have reconciled erasures.

Synthetic regression:

```sh
python3 scripts/test-classification-erasure.py
```

This provisions an unexposed disposable PostgreSQL 17 container; tests a fresh baseline and the exact parent `5619fda20e20bc56502877a5b2a24c145e1f4361` baseline plus only this migration, running schema/functions as a non-superuser migration owner. Before erasure, affected populated rows are confirmed (five audit events, three exception triples, one separately inventoried shared approver link). A real custom-format `pg_dump` is restored by `pg_restore` into another non-serving database and demonstrably resurrects those affected rows. Manual reconciliation removes them again, with zero remaining affected audit events/exception triples, retained memberships and linkable erased-state markers, and all other public-row digests and published snapshots unchanged. It also checks web/unprivileged/retired denial, marker/fence forgery, scope errors, atomic rollback after an injected post-deletion failure, duplicate/overlapping selection, replay, ordinary immutability, mismatch/history protection and a real concurrent writer waiting on the transaction lock. Backups remain in process memory and containers are removed in `finally`; no private data is used or printed.
