# Scheduling compatibility verification

This is local compatibility work for #391. It does not complete focused workout authoring, dated moves, either real-plan replay, or release acceptance. Partial-time authoring remains disabled in this stage.

The expanded database records schedule precision separately from actual timestamps. Compatible readers distinguish an exact interval, a known start with an unknown end, a day without a time, and an explicitly named morning or afternoon. They never derive planned times from distance, exercises or reported results. Frozen v1 instructions retain their original display text and serialization; v2 instructions carry their own typed schedule.

Exact-target edits remain available when unrelated sessions in the same week have partial timing. Operations consuming a whole source reject unsupported partial/v2 input atomically. Known-start outcomes retain the existing active-session, membership, replacement, version and feedback rules. Unknown-time outcomes remain explicitly unsupported in this compatibility stage. Date copies with incompatible known endpoint UTC-offset patterns require review; legacy duration-only routines retain their existing semantics.

Production startup, readiness and requests require both the database contract and a trusted external admission projection. Exact GET/HEAD liveness bypasses session storage and authentication. A copied database incarnation or floor is not proof that a restore was reconciled. See [the operational protocol](operations/training-schedule-admission.md).

## Evidence established during development

- The persistence engineer's isolated PostgreSQL suite checked populated forward/fresh schema parity, unchanged historical rows and hashes, proper-role privileges, exact parent/final admission, closed partial-write gates, target locking and state/floor races. An actual pre-expansion reader and startup verifier were exercised against the expanded database.
- The backend adapter PostgreSQL test checked independent exact-target edits in mixed plans, atomic whole-source rejection, known-start outcomes without filling a planned end, and revoked staff authority.
- The signed original-genesis-to-current migration command rehearsal retained the protected receipt and historical interval. The current fresh-genesis command rehearsal also passed.
- A routed browser trial exposed stale ordinary-session edits overwriting safe entered values. The recovery now retains the submitted fields and original optimistic version without fetching a replacement private row; focused regressions cover stale/concurrent conflict and revoked authority. Final routed verification uses the revised candidate.
- Independent QA reproduced and verified fixes for copy/move error recovery, repeated daylight-saving-time labels and liveness with an existing session cookie. Independent persistence/operations security review established no new material finding in its bounded scope.
- A compile-stamped test process exercised actual application construction and the real router against the low web role and root-owned admission file. Valid admission served readiness/login; blocked admission refused startup/serving. Cookie-bearing GET/HEAD liveness remained available after closing the application database pool. This was not an externally launched server or browser run.
- Deployment-control tests include twelve admission/helper/adapter cases and the source release-controller suite. These use isolated files and synthetic command adapters; they are not an installed deployment rehearsal.

The delivery lead retains private test logs, source hashes and handoffs. The final integrated source gate and routed reader evidence must be recorded against the assembled candidate, rather than inferred from earlier component runs.

## Outstanding acceptance

Actual routed compatibility coverage and the final integrated repository gate, and an actual future nullable-schema ledger/current-operator-to-compatible-reader fallback rehearsal remain separate evidence requirements. A test-only nullable-shape fixture does not establish a real future migration or rollout.

An inherited recipient-eligibility mismatch remains a #403/#404 finding: publication selection and insertion can include a dependant after all current verified guardian authority is lost, although roster eligibility excludes that person. Viewer permissions are separate. The fix must bind the reviewed audience, recheck eligibility at transactional save and preserve existing history. This stage does not claim that finding resolved.

Both real weekly plans still require entry and publication through the actual coach UI, with moves, craft guidance, current athlete/guardian access, revoked access and immutable history checked. Automated fixtures and axe checks do not replace coach observation, manual accessibility or a natural date-boundary test. Merge, publication, deployment and live configuration remain subject to their separate human approvals.
