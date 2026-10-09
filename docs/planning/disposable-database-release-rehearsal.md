# Disposable database release: local evidence and remaining implementation

Historical counterexample and local experiment. The subsequent implemented,
release-integrated candidate and its distinct validation script are documented
in [disposable-database-release-candidate.md](disposable-database-release-candidate.md).
Statements below describe this original rehearsal, not the later implementation.

## Outcome

The owner permits replacement of the entire current test database, including
historic test rows. Data preservation and an operator-installed schema bridge
are not release requirements. A plain in-place reset through the installed
`v1.25.9` poller is nevertheless unsafe: its EXIT handler restores the old
configuration/route, **not the old database**. The old application continues
serving and reports `/health/ready` as 200 against the replaced schema, while
its membership writer has lost its required conflict arbiter.

This change installs **no production reset hook**, no startup wipe, no host
script, no publication fence, and no new deployment platform. It implements a
repeatable local proof and a concrete alternative experiment, not a completed
production replacement release path.

## Reproduce

From the isolated checkout, with Docker and the repository Go toolchain:

```sh
python3 scripts/rehearse-disposable-release_test.py
python3 scripts/rehearse-disposable-release.py
```

The script creates and cleans its own random-prefix PostgreSQL container,
network, Compose services and local image tags. It reads no host credential
file and contacts no production service. Evidence remains under `$TMPDIR`.

Actual exercised boundaries:

- Exact `v1.25.9` source/binary/schema and its 66 synthetic ledger markers.
- Synthetic historical data is deliberately discarded by explicit **local SQL**.
  This reset is outside candidate bootstrap/migration, not a disguised migration
  hook, and must not be represented as an automatic release implementation.
- The unsupported migration role (no superuser/CREATEDB/CREATEROLE) is refused
  before destruction; the historical sentinel still exists after refusal.
- The local bootstrap administrator is superuser/CREATEDB/CREATEROLE. These are
  rehearsal capabilities, **not a live production privilege attestation**.
- Unchanged installed poller database bootstrap/migrate commands run real
  candidate Compose jobs after the local reset; the final ledger has 77 markers.
- Ordinary candidate bootstrap/migrate retries leave candidate data intact.
- Fresh web reads and protected ACL boundaries are checked, privacy activation
  remains disabled with the worker kill switch engaged, guardian intake is off.
- The established administrator CLI seeds one active administrator from a
  synthetic password file. This proves the CLI, **not automatic recovery of
  current production credentials**. No real credential is read or exposed.
- Both exact old and candidate applications are real processes. Fresh candidate
  readiness, login and embedded asset reference pass.
- A real candidate is stopped after readiness. The unchanged tagged EXIT
  recovery runs from `candidate_validation`, restores the predecessor env and
  leaves the old process running. The old process remains ready (200), but an
  EXPLAIN of its old membership writer fails because its unique arbiter is gone.
- A separate explicit local experiment provisions a distinct candidate web
  principal using the same synthetic app secret, disables/revokes the old web
  principal and terminates its sessions. The new candidate becomes ready while
  the old readiness endpoint becomes 503. Existing bootstrap/migrate routines
  support these different configured app role names without a host upgrade.

Excluded: ECR/GitHub discovery and attestation, actual release manifest selection,
production secret/config state, guardian-release-bind stage, Caddy route switch
and post-switch recovery, media workers/object storage, and a release-integrated
one-shot replacement capability. No remote responses are fabricated. The
candidate-failure test is an actual stop plus failure injected at the installed
handler's pre-switch state; it is not a claim to have executed the full poller.

## Small concrete options — no host-upgrade requirement

1. **In-place reset plus distinct candidate web role (preferred next slice).**
   The local role experiment proves the essential rollback fence without SSH,
   new host scripts, privileged mounts or a secret rotation. Candidate bootstrap
   can use its existing administrator capability; candidate config must use the
   new web principal with the established app password, rather than re-enable
   the predecessor principal. Terminate predecessor sessions before destructive
   DDL. If the candidate fails, installed rollback points to the old application
   but its database access stays denied: a deliberate maintenance outage, not
   unsafe old-schema writes. The role handoff, one-shot reset and admin recovery
   still need integration/tests in the real candidate binary and commands.
2. **Fresh named database with version-scoped routing.** Existing `DB_NAME` and
   DATABASE_URL configuration supports a named database, but the signed manifest
   has no DB switch field and the installed poller updates only image/version/
   release time/SHA. Guardian bind has its own root-file URL and expected database.
   A candidate-only routing implementation must cover *all* commands, web,
   guardian binding and jobs; changing only the web DB name is not safe. Changing
   SSM or host files is outside this task. This is therefore not an already
   supported automatic switch available merely by publishing an image.
3. **Additive compatibility.** Retaining the predecessor uniqueness constraint
   would restore its conflict arbiter but prohibit legitimate new dated intervals;
   this is not a free compatibility fix. Supporting both writers correctly would
   need an explicit compatibility contract, contrary to the requested simple
   disposable reset. Do not silently retain that constraint to pass readiness.

## Requirements before installing option 1

- Bind the capability to an explicitly reviewed candidate/version, exact expected
  66-marker predecessor digest, database identity and final inventory; refuse
  unknown, partial or later ledgers. Keep a durable completion binding outside
  the deleted schemas, so retry never erases post-reset candidate data.
- Refuse once the club's real-data lifecycle is enabled. No such lifecycle
  attestation is implemented by this rehearsal; absence of an attestation is not
  permission to wipe. Do not add a generic persistent RESET_DATABASE=true flag.
- Preflight every required owner/role/database/schema/session capability, final
  schema inputs and administrator recovery material before destruction. Avoid
  weakening gates or disabling ordinary protected-history triggers.
- Carry only explicitly approved active administrator authentication identity/
  role material using the existing stored hash/config, or arrange the established
  CLI password-file path. Do not discard all administrators and assume the poller
  will run the CLI: it does not. Do not expose hashes/secrets in release receipts.
- Rehearse actual candidate-bootstrap reset, migration, role ACLs, final markers,
  repeated polls, failures before/after reset and post-switch exact tagged EXIT
  recovery. The local role experiment alone is not this acceptance evidence.
- Explain the brief old-serving database errors during fencing/reset and the
  maintenance outage after candidate failure. Never claim seamless rollback.
- Leave retired privacy/guardian gates off. Object storage is not wiped: database
  replacement loses object references/provenance, so existing objects can become
  orphaned. Inventory and separately approve exact-prefix/version cleanup; do
  not grant blanket object deletion to make the DB reset appear complete.

No live changes, secret writes, GitHub writes, publication, merge or push occurred.
