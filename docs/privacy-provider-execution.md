# External provider and recipient erasure

Status: source-only, disabled, and intentionally inert. No subject-specific external integration or autonomous recipient is registered in production. The responsible controller confirmed on 2026-09-11 that the MyCFCoimbra server sends no profiles or other data to FPC; opening an external FPC history link makes a browser request whose destination URL contains the licence number, which is navigation rather than a server integration. This document does not authorize activation.

## Closed registry and activation boundary

The signed activation inventory and the runtime execution registry have different jobs. The v2 activation inventory may be `READY` and empty when signed evidence proves MyCFCoimbra has no active subject-specific external integration or recipient; missing, incomplete, stale or merely assumed emptiness remains not ready. `ProviderExecutionRegistry` is the runtime adapter registry, constructed once from factual registrations. Each entry binds a stable service code to exactly one role (`PROCESSOR` or `AUTONOMOUS_RECIPIENT`), an adapter contract version, a rotation-aware registry-evidence key and digest, and an installed adapter. Its empty state is intentionally not execution-ready because there is no provider operation to perform. Duplicate, malformed, unevidenced or adapter-less nonempty runtime registries are rejected. External navigation and CFC operations outside MyCFCoimbra are not entries in either registry.

The provider operation is accepted only by executor/schema v2. Historical v1 plans remain readable and cannot gain provider work. A v2 plan that contains `PROVIDER_RECIPIENT_NOTIFY` cannot start unless both the closed registry and the seal-only provider target protector are installed. Every captured service/role/contract/evidence-key/digest tuple must match the closed registry. An unknown or stale tuple aborts and rolls back the entire execution-start transaction.

Inactive wearable integrations are outside this registry and outside the protected provider connection inventory. The provider-neutral `activity_connections` foundation is not treated as an active external connection. Activating a wearable later requires a separate factual #109 registration and an explicit migration into this contract; its mere database presence never broadens #246 execution.

## Capture and quarantine

Active external integrations must first record a self-describing encrypted target and the minimum encrypted revocation credential in `privacy_protected.provider_connections`. Ordinary web roles cannot read that schema. The source state is the concurrency boundary for sync, webhook, credential refresh, and reconnect behavior.

Within the same transaction that creates the immutable #244 execution graph, capture:

1. locks every subject connection and rejects a prior unresolved quarantine;
2. switches active rows to `QUARANTINED`, increments their state version, disables sync/webhook/reconnect, commits the credential through a dedicated rotation-aware HMAC key, and removes the source credential;
3. seals target and credential independently with randomized X25519/HKDF/AES-GCM envelopes whose authenticated binding includes execution, job, checkpoint, plan-entry digest, category, service, target kind, provider role, target version, operation, action version, and provider contract version;
4. stores separately keyed, rotation-aware target and credential commitments plus the closed-registry evidence binding;
5. verifies the captured target count before account access can be cut off.

Any failure rolls the transaction back, including the source fence and credential move. Disconnected source rows are captured without a credential so the remote adapter can verify the idempotent already-disconnected outcome. Target locators, source key identifiers, credentials, and provider evidence references never enter public tables, request events, application logs, metrics, or error strings.

## Worker and evidence

Only the separately configured worker can list provider targets through the lease/attempt/epoch-fenced routines. It opens the execution-bound envelopes, reconciles the sealed registration tuple against its own closed registry, and invokes the exact adapter with a per-call timeout and at most five local attempts (three by default). Remaining retry scheduling is owned by the existing bounded job lifecycle.

Accepted processor outcomes are `REMOTE_DELETION_VERIFIED` and `ALREADY_DISCONNECTED`. `NOT_CONTROLLABLE` is never accepted for a processor. A registered autonomous recipient may use `NOT_CONTROLLABLE` only with all of the following allowlisted structured evidence: recipient role, delivery channel, accepted/delivered notification outcome, reason, requester guidance, and an opaque evidence reference. The reference is included only in a separately keyed evidence transcript digest; it is never persisted verbatim.

The database repeats role/outcome/allowlist checks under the active lease fence. Evidence insertion, source-connection removal, and credential-quarantine deletion occur together. The checkpoint succeeds only when every frozen target has evidence and no quarantine remains. A stale worker cannot record evidence. Corrupt envelopes, authentication failures, unsupported roles/outcomes, missing evidence, unknown adapters, and exhausted retries remain blocking failures.

There is intentionally no generic terminal-failure credential purge. A future #248 operator path may purge a quarantine only after separately approved terminal handling and must preserve the same evidence and authorization boundaries. Until then, unresolved quarantine remains recoverable and activation-blocking.

## Rollout concerns

- Keep migration `202609100009_privacy_provider_execution.sql` ordered after the authenticated replay `008` migration; both forward-migration and fresh-baseline checks must remain green as later migrations land.
- If a subject-specific integration is introduced, update #109 and the signed inventory, then add its factual registration and adapter before activation; never infer services from infrastructure alone.
- Implement the source-specific write/fence integration for each approved active service; direct insertion is not an activation procedure.
- Provision independent encryption, target-digest, credential-commitment, registry-evidence, and evidence-transcript key identifiers with documented rotation and custody.
- Grant web and worker roles only the exact capture/materialisation or fenced worker functions they need; never grant protected-table access.
- Run fake-adapter, PostgreSQL race/rollback, role-isolation, and complete repository verification before proposing activation.
- Keep privacy requests, provider capabilities, worker scheduling, and all live provider actions disabled until the separate human gates are satisfied.
