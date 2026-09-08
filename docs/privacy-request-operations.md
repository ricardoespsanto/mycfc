# Privacy-request operations

This runbook covers the #110 intake and review workflow. Cross-store erasure and account closure execution belong to #111. An approval remains awaiting execution and preserves account access; it is never evidence that erasure completed.

## Activation prerequisites

The application setting `PRIVACY_REQUESTS_ENABLED` defaults to `false`. Database activation also remains unavailable until #111/#248 supplies validated live executor capabilities and an evidence digest; there is no operator override. Before production activation, the club must adopt the exact machine-executable category catalogue and retention grounds, exclude every unresolved category or provider fail-closed, configure the approved 90-calendar-day identifiable working-record period, designate independent reviewers, verify active provider evidence, and complete, test and evidence the #111 executor. A manual fulfilment route is not sufficient. The internal matrix groups approved on 7–8 September 2026 do not by themselves verify implementation, external legal exceptions or provider facts.

The browser fixture in `scripts/e2e-privacy-seed.sql` is synthetic and restricted to `mycfc_test`. Its catalogue and reviewer accounts are test data, with no production authority.

## Operator commands

Run the deployed server binary with its normal configuration and database access. Every command requires `--actor` with the UUID of an active adult platform administrator. The operator role can provision capabilities; an ordinary administrator still cannot read or review cases. Changes to grants and activation append actor audit events; retention exceptions are themselves immutable actor records.

```sh
mycfc privacy grant --actor ADMIN_UUID --user REVIEWER_UUID
mycfc privacy revoke --actor ADMIN_UUID --user REVIEWER_UUID
mycfc privacy import-policy --actor ADMIN_UUID --file /secure/path/adopted-policy.json
mycfc privacy deactivate --actor ADMIN_UUID --policy ADOPTED_VERSION
mycfc privacy hold --actor ADMIN_UUID --reference CASE_REFERENCE_UUID --owner PRIVACY_REVIEWER_UUID --category identity-core --reason LEGAL_HOLD --evidence OPAQUE_EVIDENCE_REFERENCE
mycfc privacy expire --actor ADMIN_UUID
```

These are command templates, not executed production changes. A grant never permits self-review of a requester or subject case. Public activation now fails closed unconditionally: #243 can compile a decision plan, but only #111/#248 may add a validated live executor capability and evidence digest. A command-line assertion cannot stand in for that implementation. Deactivation remains available for an existing activation record.

The current policy JSON has `version`, the exact supported `executor_version` and `plan_schema_version`, `categories`, `account_closure_enabled`, `working_retention_days`, `response_months` and `extension_months`. Each category has an immutable `key`, member-facing `label` and `description`, a server-known execution `rule`, and optional approved grounds with their own restriction/expiry rule. Rules reference a closed execution profile and contain a legal-ground code, accountable owner-role code, deadline/review/expiry periods and `BLOCK` fallback; restricted rules also name the exact allowlisted retained-field codes. The server, never the browser, expands those rules into sorted operation codes, an exact decision-anchored execution deadline, and server-owned retention anchor/calendar-day instructions. Exact review/expiry timestamps are persisted only when the anchor already exists (currently a refused case's closure); record-, consent-, session- and future-closure anchors remain unresolved and fail closed until #111 resolves them from the real source event. Unknown versions, profiles, fields, anchors or incomplete instructions fail import, intake, decision and activation. The application fixes response and extension windows at one and two calendar months respectively. Set `working_retention_days` to the approved value of `90`; any change requires a new product-policy decision. Import a new version for other changes; existing receipts retain their adopted snapshot. Supply only actually approved and executable categories and periods.

## Case handling

Members enter through their profile and `/perfil/privacidade`. Submission requires password reconfirmation and rejects stale credentials. Minors receive the age-appropriate rights contact. A named guardian may receive a minimal receipt, but the current relationship alone does not prove legal representation or permit protected disclosure.

Explicit reviewers use `/admin/privacidade`, claim a case, verify identity and representation as applicable, and record a bounded explanation with category outcomes and approved retention grounds. The receipt date remains fixed through verification. Deadlines use Lisbon calendar arithmetic; an explained extension must be recorded within the original response window. Stale forms are rejected and must be reloaded.

Closure approval requires current dependant resolutions and protection of the last active administrator. Full and partial approvals remain open pending fulfilment. There is no start-processing route in this implementation. Cancellation and refusal close a case and start its evidence-retention period.

Acknowledgement and update emails use the existing durable SMTP outbox and encryption key. Notices contain a public rights contact route and no case reference, names, category details or explanation. Provide the protected explanation through an appropriately verified contact channel if account access later ends. Never copy case contents or recipient addresses into operational logs.

## Retention and rollback

Run `privacy expire` under the club's approved operational schedule. It removes identifiable working material 90 calendar days after case closure, according to the receipt's saved working-record period, and purges expired minimal case evidence after 24 calendar months. Record a category complaint or legal-hold exception on a refused case before expiry with `privacy hold`. The category, purpose, exact retained-field allowlist, review date and expiry are copied from that case's immutable decision plan; the operator supplies only its matching bounded reason, opaque evidence reference and an active adult privacy reviewer as accountable owner. A category exception never extends the 90-day identifying working copy: that material is scrubbed on schedule. It preserves the minimal case/plan evidence needed to govern the future #111 category treatment only until its plan-defined expiry. Extending it requires a new policy/decision basis rather than an arbitrary operator date. Cancellation has no category decision plan and cannot receive this kind of hold; any separate legal record must be handled under a separately approved legal-record process. The command must leave open awaiting-execution cases and reviewer/activation audit intact. It reports counts only. Do not treat subject-data erasure as accomplished by this maintenance operation.

The workflow foundation uses `202609070001_privacy_requests.sql`; the forward-only #243 plan/retention additions use `202609080001_privacy_execution_plans.sql`. Fresh installations use the matching complete baseline. Apply migrations through the existing migration command only under the release procedure. A rollback to an application version without privacy outbox or execution-plan support leaves privacy notifications/plans in place: old code must not reinterpret or remove them. Account for the backlog and response deadlines before rollback, and restore a compatible worker promptly. Do not delete pending notices, plans, or reset the live database to resolve a downgrade.

## Verification boundary

Release evidence is recorded in `docs/acceptance-matrix.md`. Synthetic tests establish application behavior, not policy adoption, reviewer appointment, completed #111 execution evidence or live migration approval.
