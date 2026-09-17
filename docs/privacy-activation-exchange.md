# Privacy activation dual-signer exchange

The activation broker accepts one canonical material document and two independent P-256 signatures: `EXECUTOR` and `ADMINISTRATOR`. The humans approve through separate protected GitHub environments and separate non-exportable AWS KMS keys. No SSH session, downloadable private key, generic host command, or manual approval-file staging is part of the ceremony.

## Provisioning and fixed identities

Enable `privacy_activation_exchange_enabled` only in a reviewed production Terraform plan. The retained AWS root creates:

- one private S3 exchange bucket with versioning, Object Lock COMPLIANCE retention, exact KMS encryption, checksum and create-only write enforcement, and two-day current/noncurrent expiry;
- one separate CloudTrail bucket with a 90-day lifecycle and object data events limited to the exchange `ceremonies/` prefix;
- one host courier IAM user with exact material/receipt writes and exact approval-version reads, without list, delete, signing, secret, state, or role-chaining access;
- separate executor and administrator OIDC roles, each able to read the material, write only its own fixed approval path, and sign only with its own `ECC_NIST_P256` KMS key;
- one production-environment coordinator role that can read material and receipts but cannot write approvals or sign;
- a separate release-agent policy that manages access keys only for the exact courier user and explicitly denies every other user and role chaining.

The signer workflows use fixed roles:

```text
arn:aws:iam::334960985019:role/mycfc-production-privacy-activation-exchange-executor
arn:aws:iam::334960985019:role/mycfc-production-privacy-activation-exchange-administrator
```

Configure `privacy-activation-executor` and `privacy-activation-administrator` as separate GitHub environments with required reviewers, administrator bypass disabled, and protected `main` deployment rules. Each environment needs its signer-specific numeric actor ID, signing-key ARN, the common exchange bucket and KMS encryption-key ARN, the canonical registry SHA-256, and the canonical registry JSON secret. The repository does not invent human actor IDs or actor UUIDs.

After Terraform apply, obtain both public keys with `aws kms get-public-key`, decode `PublicKey` into DER, and compute each lowercase SHA-256. Construct canonical compact JSON in the field order below with no trailing newline. The numeric IDs shown are examples and must be replaced with the two real, distinct GitHub actor IDs:

```json
{"contract":"mycfc/privacy-activation-signer-registry/v1","signers":{"EXECUTOR":{"actor_ref":"<executor-actor-uuid>","signing_key_id":"<executor-key-id>","kms_key_arn":"<executor-kms-key-arn>","public_key_spki_sha256":"<executor-der-sha256>","github_actor_id":123456789,"github_environment":"privacy-activation-executor"},"ADMINISTRATOR":{"actor_ref":"<administrator-actor-uuid>","signing_key_id":"<administrator-key-id>","kms_key_arn":"<administrator-kms-key-arn>","public_key_spki_sha256":"<administrator-der-sha256>","github_actor_id":987654321,"github_environment":"privacy-activation-administrator"}}}
```

Install the registry and DER files as root-owned regular mode-`0600` files:

```text
/etc/mycfc/privacy-activation/signer-registry.json
/etc/mycfc/privacy-activation/executor-public-key-spki.der
/etc/mycfc/privacy-activation/administrator-public-key-spki.der
```

Pin the canonical registry digest separately in `/etc/mycfc/privacy-activation-exchange.env`:

```dotenv
PRIVACY_ACTIVATION_EXCHANGE_ENABLED=true
AWS_REGION=eu-west-1
PRIVACY_ACTIVATION_EXCHANGE_BUCKET=<terraform-output>
PRIVACY_ACTIVATION_EXCHANGE_KMS_KEY_ARN=<terraform-output>
PRIVACY_ACTIVATION_COURIER_EXPECTED_ARN=arn:aws:iam::334960985019:user/mycfc-production-privacy-activation-exchange-courier
PRIVACY_ACTIVATION_CREDENTIAL_ADMIN_EXPECTED_ARN=arn:aws:iam::334960985019:user/mycfc-production-release-agent
PRIVACY_ACTIVATION_SIGNER_REGISTRY_SHA256=<canonical-registry-sha256>
PRIVACY_ACTIVATION_POLICY_VERSION=club-2026-09-15-v1
```

The file must be root-owned mode `0600`. The runtime accepts only these keys and validates every value before sourcing it.

## Courier credential lifecycle

The already installed `/etc/mycfc/release-aws/credentials` profile `mycfc-release` is the only credential administrator. It remains root-owned mode `0600`. Through the protected production operation workflow:

1. Dispatch `activation-courier-provision` once. It refuses an existing courier key, creates one, verifies its exact caller ARN, and atomically writes `/etc/mycfc/privacy-activation-exchange/aws-credentials` mode `0600`.
2. Dispatch `activation-courier-rotate` to create and validate a replacement before deactivating and deleting the previous key.
3. Dispatch `activation-courier-revoke` to deactivate and delete every courier key and remove the local profile. This also requires the destructive gate.

The scripts never print a secret access key. Terraform manages the courier identity and policy only, so courier key material never enters Terraform state. If rotation stops after installing the new local key but before deleting the old key, rerun the reviewed rotation/revocation recovery after inspecting only access-key IDs and statuses; AWS permits at most two keys.

## Ceremony

1. Record and review the current activation evidence set. Enable the general, destructive, and activation host gates for the approved window.
2. Dispatch `activation-ceremony-open` from the protected production workflow with the exact active signed, CI-green `main` SHA, immutable image, and evidence-manifest SHA-256.
3. The host broker prepares canonical material valid for at most 15 minutes. The courier uploads it once to `ceremonies/<ceremony-id>/material.json` with SHA-256, exact KMS encryption, Object Lock, and `If-None-Match: *`. The collector timer starts only after the local state is committed atomically.
4. Review the coordinator summary. Dispatch each signer workflow with the exact ceremony ID, material version ID, material SHA-256, source SHA, image digest, and schema migration digest before expiry. Each signer independently verifies the numeric GitHub actor, exact current signed `main` commit, successful canonical CI, material version/checksum, registry digest, and release bindings before requesting OIDC. KMS signs a raw SHA-256 digest with `ECDSA_SHA_256`; the private key is never exportable.
5. The host collector reads only the two exact fixed approval keys. It does not list the bucket. After both are present it downloads their exact versions, verifies KMS/checksum metadata, stages them atomically as mode `0600`, runs the signing-only bundle verifier without a network, rechecks expiry, and invokes `activate-exchange` through the database broker.
6. The host writes and uploads a privacy-safe receipt containing hashes, object version IDs, release bindings, result, reason, and timestamps. It removes active approval material and stops the timer. Expiry, a malformed latest object, checksum/version mismatch, wrong signer, or broker rejection closes the ceremony without activation. Open a fresh ceremony after expiry.

The exchange objects are fixed:

```text
ceremonies/<uuid>/material.json
ceremonies/<uuid>/approvals/executor.json
ceremonies/<uuid>/approvals/administrator.json
ceremonies/<uuid>/receipt.json
```

No workflow summary contains the registry JSON, actor UUIDs, signatures, approval envelopes, credentials, or database URLs.

## Rollback and recovery

Before activation, dispatch courier revoke or let the 15-minute material expire; the collector fails closed and cleans local active state. After activation, use the existing fixed rollback order: `activation-disable`, `worker-disable`, then `flags-disable`. Confirm readiness is blocked and the worker is inactive. Disabling or removing exchange infrastructure does not reverse a database activation, and Object Lock prevents immediate deletion of ceremony evidence. Preserve the receipt and CloudTrail events for review.
