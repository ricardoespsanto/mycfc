# Privacy-request operations

This runbook covers the #110 intake and review workflow. Cross-store erasure and account closure execution belong to #111. An approval remains awaiting execution and preserves account access; it is never evidence that erasure completed.

## Activation prerequisites

The application setting `PRIVACY_REQUESTS_ENABLED` defaults to `false`. Database activation independently requires an imported, immutable adopted policy and an explicit fulfilment-readiness assertion. Before production activation, the club must adopt the exact category catalogue and retention grounds, approve the identifiable working-record period, designate independent reviewers, and evidence an available executor or separately approved manual fulfilment route. The approved 24-calendar-month minimum-evidence policy does not adopt the wider retention matrix.

The browser fixture in `scripts/e2e-privacy-seed.sql` is synthetic and restricted to `mycfc_test`. Its categories, reviewer accounts and 30-day working-record period are test data, with no production authority.

## Operator commands

Run the deployed server binary with its normal configuration and database access. Every command requires `--actor` with the UUID of an active adult platform administrator. The operator role can provision capabilities; an ordinary administrator still cannot read or review cases. Changes to grants and activation append actor audit events.

```sh
mycfc privacy grant --actor ADMIN_UUID --user REVIEWER_UUID
mycfc privacy revoke --actor ADMIN_UUID --user REVIEWER_UUID
mycfc privacy import-policy --actor ADMIN_UUID --file /secure/path/adopted-policy.json
mycfc privacy activate --actor ADMIN_UUID --policy ADOPTED_VERSION --fulfilment-ready
mycfc privacy deactivate --actor ADMIN_UUID --policy ADOPTED_VERSION
mycfc privacy expire --actor ADMIN_UUID
```

These are command templates, not executed production changes. Activation requires at least two active adult reviewers so an independent reviewer is available for conflicts. A grant never permits self-review of a requester or subject case. The `--fulfilment-ready` flag records an operator assertion; it does not install or test an executor.

The policy JSON has `version`, `categories`, `account_closure_enabled`, `working_retention_days`, `response_months` and `extension_months`. Each category has an immutable `key`, member-facing `label` and `description`, an approved `action` code, and `grounds` containing approved `code`/`label` pairs. The application fixes response and extension windows at one and two calendar months respectively. Import a new version for changes; existing receipts retain their adopted snapshot. Supply only actually approved categories and periods.

## Case handling

Members enter through their profile and `/perfil/privacidade`. Submission requires password reconfirmation and rejects stale credentials. Minors receive the age-appropriate rights contact. A named guardian may receive a minimal receipt, but the current relationship alone does not prove legal representation or permit protected disclosure.

Explicit reviewers use `/admin/privacidade`, claim a case, verify identity and representation as applicable, and record a bounded explanation with category outcomes and approved retention grounds. The receipt date remains fixed through verification. Deadlines use Lisbon calendar arithmetic; an explained extension must be recorded within the original response window. Stale forms are rejected and must be reloaded.

Closure approval requires current dependant resolutions and protection of the last active administrator. Full and partial approvals remain open pending fulfilment. There is no start-processing route in this implementation. Cancellation and refusal close a case and start its evidence-retention period.

Acknowledgement and update emails use the existing durable SMTP outbox and encryption key. Notices contain a public rights contact route and no case reference, names, category details or explanation. Provide the protected explanation through an appropriately verified contact channel if account access later ends. Never copy case contents or recipient addresses into operational logs.

## Retention and rollback

Run `privacy expire` under the club's approved operational schedule. It removes expired closed-case working material according to the receipt's saved working-record period, and purges expired minimal case evidence after 24 calendar months. It must leave open awaiting-execution cases and reviewer/activation audit intact. The command reports counts only. Do not treat subject-data erasure as accomplished by this maintenance operation.

The additive migration is `202609070001_privacy_requests.sql`; fresh installations use the matching complete baseline. Apply through the existing migration command only under the release procedure. A rollback to an application version without privacy outbox support leaves privacy notifications pending: the old worker cannot claim those message kinds. Account for the backlog and response deadlines before rollback, and restore a compatible worker promptly. Do not delete pending notices or reset the live database to resolve a downgrade.

## Verification boundary

Release evidence is recorded in `docs/acceptance-matrix.md`. Synthetic tests establish application behavior, not policy adoption, reviewer appointment, production fulfilment readiness or live migration approval.
