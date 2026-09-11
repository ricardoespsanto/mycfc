# Privacy retention completion migration 010

The migration is additive except for replacing the existing internal `privacy_retention_run(uuid,integer)` signature in the same transaction. No application route calls that function, the maintenance timer is disabled by default, and database bootstrap reapplies grants before migration commit. Existing rows are preserved; new consent columns are nullable, the validated constraint accepts every legacy row, and no historic audit or repair row is rewritten during migration.

Consent cessation is immutable once recorded. Evidence deletion locks candidates and skips the only live foreign-key reference. Audit pseudonymisation and repair queuing use bounded ordered candidates; repair source locking and the existing deferred provenance invariant serialize pointer changes. The single advisory transaction lock makes concurrent maintenance calls fail without partial work.

Rollback is compensating and forward-only. Disable the maintenance timer and keep the schema and immutable evidence in place. Do not drop cessation columns, pseudonymous references, repair cleanup events, or run records. A later migration may replace a faulty routine after compatibility review, but must not restore identifiers or claim object absence without the protected exact-version evidence.
