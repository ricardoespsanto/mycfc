# Production database host compatibility candidate (not yet installed)

This is a narrowly scoped bridge for the installed `mycfc-production` Compose
bundle, not a replacement for that bundle and not a database reset command.
The installed release poller has privacy-worker and restore-gate behaviour absent
from the signed source poller; do not overwrite it as part of this repair.

## Exact observed boundary

- The host's `db-bootstrap` service does not receive `POSTGRES_PASSWORD`; its
  `migrate` service does not receive `MIGRATION_DB_PASSWORD`. PostgreSQL alone
  receives its privileged password from `/etc/mycfc/mycfc.env`. The two keys are
  present in that protected legacy file, but no values were inspected.
- Neither `/etc/mycfc/database-maintenance.env` nor
  `/etc/mycfc/database-control.env` exists on the host. The installed bundle
  does not need the latter for this compatibility bridge. The former must be
  provisioned under approved secret custody before this candidate can render.
- The installed Compose project is `mycfc-production`; its PostgreSQL container
  mounts only `mycfc-production_pgdata` at `/var/lib/postgresql/data`. Do not
  use `docker compose down -v`: unrelated uploads and volumes are out of scope.
- The signed `v2.0.1` image requires the exact populated predecessor ledger and
  refuses a newly initialized empty database. The app engineer's *local,
  unreleased* `b20bc42` candidate adds an explicit `fresh-init-db --confirm-empty`
  command; see `docs/planning/fresh-database-init.md` in that worktree. Do not
  reset the volume or retry `v2.0.1` solely because this Compose candidate
  renders. The new binary and host compatibility change need reviewed release
  integration before any reset.

## Local candidate (no live write)

Use `scripts/prepare-installed-compose-db-compat.py` on a read-only copy of the
installed Compose file with a new output path. It requires the exact observed
bootstrap and migrate stanzas; unknown drift or an existing candidate is a
refusal. Only the two release-service stanzas change:

- `db-bootstrap`: existing `POSTGRES_PASSWORD` and `MIGRATION_DB_PASSWORD`
  interpolations from the legacy protected env, plus required maintenance file.
- `migrate`: only `MIGRATION_DB_PASSWORD` from the legacy protected env, plus
  the same required maintenance file. The superuser password is not passed to
  migrate, app-blue, or app-green. PostgreSQL and all other services are unchanged.

The preparer neither reads credential files nor writes a host path. The local
candidate is created with exclusive mode `0600`; review a source-only diff. The
observed candidate adds exactly these service mappings and nothing else.

    python3 scripts/prepare-installed-compose-db-compat.py INSTALLED_COPY CANDIDATE
    MYCFC_INSTALLED_COMPOSE_FIXTURE=INSTALLED_COPY python3 scripts/prepare-installed-compose-db-compat_test.py

The test suite checks drift refusal, exclusive output, input preservation, and
Docker Compose `config --quiet` with synthetic placeholders. Never run `docker
compose config` without `--quiet` against production credentials; normal output
would disclose their values. Do not paste protected diagnostics into chat.

## Credential custody and bounded future installation

The signed source install contract requires one root-owned, non-symlink `0600`
`/etc/mycfc/database-maintenance.env` containing **only** these four keys:
`MEDIA_CLEANUP_DB_USER`, `MEDIA_CLEANUP_DB_PASSWORD`,
`DATA_RETENTION_DB_USER`, `DATA_RETENTION_DB_PASSWORD`. The observed legacy env
has none; the expected dedicated file and legacy media-cleanup/retention files
are absent. There is therefore **no established source or custody** for those
values. Obtain them from an owner-approved secure source or provision new
fresh-database role credentials under an expressly approved secret-custody path;
never copy values through chat, command arguments, Compose output, logs, or a
workstation artifact. Do not invent role identities, assume old values, or rotate
other credentials. The two privileged values already in `mycfc.env` remain
there unchanged for the bridge. New file staging and role creation are *not*
authorized by this preparation.

Before any host change, verify the exact live Compose source still matches the
reviewed candidate input, identify and fence the release poller and other DB
writers under the applicable live-operation approval, and establish an
owner-reviewed fresh-DB image that initializes with the same role contract.
Preserve an exact private backup of the host Compose file; then stage the
maintenance file with root:root `0600` and only the four allowed keys, and
atomically replace only the Compose file after a silent `config --quiet` check.
This is an execution procedure for later authorization, not an instruction to
run it now. Keep the private backup until post-release verification.

Nonmutating validation must report fixed pass/fail only: check file ownership,
regular-file status and exact key *names* without values; Compose
`--profile release config --quiet` with stdout and stderr protected; and a
password-authenticated `SELECT 1` using the configured PostgreSQL role and
existing protected credential with `default_transaction_read_only=on`. The
`pg_isready` check is not authentication. Never run `bootstrap-db` or `migrate`
as a probe: the former can execute destructive DDL. A previous auth-only probe
was **not run** because the local terminal approval control blocked it; do not
bypass that control. Release retry and quarantine clearing need their own exact
scope after the fresh-init path has passed tests.

## Integration with the unreleased fresh-init app path

The locally tested `b20bc42` app branch adds **only** the explicit
`fresh-init-db --confirm-empty` command; it does not turn `bootstrap-db` into
fresh initialization. Its `runFreshInitCommand` uses `DATABASE_URL` if present;
otherwise it loads AWS production configuration, builds the superuser URL from
`POSTGRES_USER` (SSM) and `POSTGRES_PASSWORD` (the service environment), and
uses app password (runtime secret), migration password (service environment),
and all four maintenance fields (dedicated env file). The compatibility
candidate supplies only the locally missing fields; it does not replace AWS
configuration. The stamped `APP_VERSION`/`GIT_SHA` and exact database must match
the *new* signed binary, not `v2.0.1`.

Before the new image becomes host-visible, a later approved host operation must
fence the old poller and other DB writers. After the reviewed/signed candidate
is published and independently identified without automatic pickup, that
operation would perform the separately approved **exact** DB reset (never
blanket `down -v`), then start only the same-project PostgreSQL service with
its existing protected credential. An auth-only `SELECT 1` must pass before
DDL. With the approved new image identity staged in Compose interpolation
(never a guessed or mutable tag), the **one-time** initialization command is:

    docker compose --env-file /etc/mycfc/mycfc.env -f /opt/mycfc/deployment/compose.yaml --profile release run --rm --no-deps db-bootstrap fresh-init-db --confirm-empty

`docker compose run SERVICE COMMAND` overrides the service's normal
`bootstrap-db` command; `--no-deps` avoids silently starting other services.
This is a future, destructive database-initialization command, **not** a probe
or permission to run it now. A separate non-mutating `SELECT 1` remains the
connection test. If init fails after partial DDL, stop: it rejects nonempty
retry, and a new recovery decision is necessary. Verify the private fresh
receipt and exact ledger via the new app's supported checks before the ordinary
poller can run `bootstrap-db`/`migrate`; a matching signed image and tested
host receipt still do not alone prove traffic cutover.

A real adult administrator must be created with the existing `cmd/admin create`
using owner-supplied identity and protected password file/interactive input,
then authenticates before traffic cutover. The current production Dockerfile
builds only the server, media-cleanup, data-retention and guardian-activation
binaries; **it does not include `cmd/admin`**. Production administrator creation
therefore needs a separately tested approved execution path (for example, an
explicitly built and attested admin binary in the *new* image), not an assumed
host command, a synthetic identity, or a password in the shell. This is a
release-integration prerequisite, not a change to the narrow Compose renderer.

Rollback **before** database work is to restore the exact backed-up Compose
file and remove only the newly staged maintenance file if it was newly created
and its custody policy permits removal. Preserve the legacy env, puller, active
route, PostgreSQL volume, uploads, and other volumes. After a database reset,
configuration rollback cannot restore lost data; that is why the fresh-init
path and controlled credentials must be verified first.
