# Privacy-erasure execution operations

Issue #244 provides the execution control plane, not the destructive category handlers. Keep `PRIVACY_REQUESTS_ENABLED=false` in production until #248 supplies, tests and approves every handler and the completion-evidence verifier.

## Security boundary

- A privacy executor is an explicit capability. Administrator or reviewer access does not imply it.
- The executor who starts a case must differ from the requester, data subject and deciding reviewer.
- The web role may create one complete immutable execution/job/checkpoint graph and perform the atomic account-access cutoff. It may not claim or mutate worker state.
- The worker role has no direct table mutation privileges. It may read the immutable execution context and advance jobs only through fixed-search-path, database-clock `SECURITY DEFINER` routines that fence every mutation by worker, lease, attempt and epoch. It cannot edit decisions or plans, grant itself authority, or read subject data.
- Never place the worker credential in the web application secret set. Provision it only where the future #248 worker runs.

## Deployment order

1. Back up and verify the database in the normal release procedure.
2. Bootstrap the application, migration and optional privacy-worker roles.
3. Apply migrations through `202609080003_privacy_worker_api` with the migration role. The migrate command applies the schema and privacy privilege boundary in one transaction; do not split those steps.
4. Reapply the idempotent hardening command after migration as a defence-in-depth release check. Do not serve a candidate until it succeeds.
5. Verify that the web role cannot mutate worker-owned state and that the worker role cannot alter requests, plans, grants or unrelated tables.
6. Keep privacy-request production activation disabled. A schema deployment alone must not start work.

The migration is additive and does not enqueue existing approved requests. Rollback is a compensating forward migration; do not delete execution, lease, attempt, checkpoint, failure or access-revocation evidence.

## Starting execution

The restricted case screen displays the immutable plan and requires an explicit confirmation. Start rechecks the stored plan digest and supported versions, executor grant, four-eyes separation, historical reviewer authority, current identity and representation, current dependants and administrator continuity. Account closure also refuses to start while a live legacy session cannot be tied to a subject.

The transaction first creates and counts the entire execution graph. Only then may it move the request to `PROCESSING`. For account closure, the same transaction disables authentication, invalidates indexed sessions and pending credentials/tokens, revokes grants, records metadata-only revocation evidence and queues one generic processing-start notice. Category-only execution does none of those access-cutoff actions.

An idempotent replay for the same request and exact plan returns the existing execution. A conflicting version, changed fact or divergent plan fails without a partial graph or access cutoff.

## Worker incidents

Every worker mutation routine is fenced by job, lease, attempt, epoch and worker identity and uses the database clock. A stale or expired lease must be treated as lost; never continue destructive work after that response. Retriable failures use bounded backoff and attempts. Exhaustion or a non-retriable invariant failure becomes terminal and requires investigation.

Store only allowlisted stage/failure codes and diagnostic digests. Do not put names, email addresses, category payloads, raw errors or provider responses into execution evidence or logs.

Useful incident checks are:

- request and execution state;
- the immutable plan digest and work-set counts;
- current lease epoch, expiry and attempt number;
- incomplete checkpoints;
- structured failure classification/code and diagnostic digest;
- whether the generic notification remains pending or failed.

`SUCCEEDED` execution is not proof that the legal request is complete in #244. Until #248 verifies all destructive-handler evidence and closes the case, the request must remain open and the user-facing surface must not say that erasure is complete.
