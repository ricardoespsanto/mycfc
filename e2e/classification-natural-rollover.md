# #338 same-person natural Lisbon date acceptance

This test-only runner verifies one dependent's genuine administrator UI-confirmed
next-day participation transition on an unchanged clock. It reuses the existing
synthetic crew fixture, authorization guards, publication helpers and pinned
Playwright runtime. It does not change application code, schema, triggers, host
clock, membership dates after UI submission, release configuration or production.

## Run

Use a clean isolated source worktree and a new private evidence directory. Go,
Docker, Python 3 and the source lockfile's exact Playwright/axe dependencies are
required. PostgreSQL is pinned to `postgres:16.9`; Chromium uses the existing
`mcr.microsoft.com/playwright:v1.63.0-noble` image. If dependencies are shared from
another checkout, use its absolute directory (the runner checks both direct
Playwright package versions). No lockfile is changed:

```sh
python3 e2e/test-natural-rollover-runner.py
MYCFC_ROLLOVER_NODE_MODULES=/absolute/path/to/node_modules \
  python3 e2e/run-classification-natural-rollover.py begin /tmp/mycfc-338-natural-evidence
```

`begin` creates a uniquely named, ownership-labeled PostgreSQL container with
loopback-only port and `Europe/Lisbon` timezone, fresh baseline and migration
inventory, then loads `crew-transition-seed.sql`. It seeds synthetic minor
credentials on the existing dependent and a completed historical outcome; these
are fixtures, not guardian verification, credential issuance or outcome UI tests.

A phase-local application is compiled from the source and pinned by binary
SHA-256. The real admin UI publishes the historical prescription. Coach search
selects that same dependent; an administrator previews and confirms that person's
tomorrow transition from Leisure to Initiation with the approved synthetic
escalão. Both the member and guardian then see the old current scope, while the
new scope is still unavailable. Historical event/training/publication/outcome
rows and every column of both participation intervals are captured. The end of
`begin` must still be on its initial Lisbon date.

The application stops after `begin`; **only the isolated PostgreSQL container and
private evidence remain**. There is no sleeping process, scheduled job, or
background browser. `state.json` records the actual next-day start date.

On **that exact real Lisbon date**, run:

```sh
python3 e2e/run-classification-natural-rollover.py finish /tmp/mycfc-338-natural-evidence
```

An early, late, incomplete or repeated finish refuses before any application
traffic/write. Host/database clocks must agree; the database's timezone must
still be Lisbon. The runner refuses changed app inputs, runner/spec/helper/seed/
lockfile, binary, participation rows or historical evidence. The unchanged saved
application binary starts with fresh sessions. The real admin UI publishes the
future former-scope session; an independent current member proves its URL exists.
The original dependent and guardian now reach the new scope, lose former-future
event/training URLs and retain direct historical event/prescription access.
Interval rows and historical records must remain identical, and that dependent
must receive zero future prescriptions in the former scope. One passing attempt
per Playwright phase is required, with no skips or retries.

The accepted phase is recorded **before** future publication starts, so a failed
finish cannot automatically replay writes. Inspect the evidence and use a new
fresh run if needed. A failed begin similarly needs explicit cleanup and a new
evidence directory; no partial fixture is silently reused or reset.

After reviewing evidence, cleanup is explicit:

```sh
python3 e2e/run-classification-natural-rollover.py cleanup /tmp/mycfc-338-natural-evidence
```

Only the exact labeled, token-matching PostgreSQL container and its anonymous
volume are removed. Evidence remains. Never delete or edit `state.json` to force
a gate. Do not run this against a shared database.

## Evidence and limits

`state.json` contains source SHA, all app input file hashes (including uncommitted
source), browser/runner/seed/lock hashes, binary digest, both observed real clock
phases and full original/UI-created interval rows. `retained-before.json` keeps
complete historical records and stored prescription snapshots/hashes; the three
Playwright phase JSON/log/artifact sets and screenshots describe actual traffic.
`summary.json` is emitted only after a successful finish. Files include synthetic
identity/session traces and stay in the private local evidence directory.

This proves the same UI-created interval's read behavior before/after the actual
Lisbon date boundary **with a phase-local application restart and fresh logins**.
It does not establish continuously live sessions, same-cookie rollover, missed
midnight worker execution, summer/DST boundaries, every audience consumer,
privacy erasure/restore, or production acceptance. A begin pass alone is not
rollover acceptance. No simulated clock or forced-row equivalent is presented as
natural-clock evidence. Manual screen-reader, forced-colour and real-browser zoom
acceptance remains separate; use `docs/accessibility-manual-check.md`.

## Current local rehearsal — 9 November 2026

Branch: `test/338-natural-rollover`, worktree
`/home/ricardoes/Projects/mycfc-338-natural-rollover`. The source is based on
`08ee115b42c1a26c1ca6971f5036300d468bb1f0` with the local field-error accessibility
fix; the private state records all tested app-input hashes rather than claiming
the base commit alone identifies the binary.

Evidence: `/tmp/mycfc-338-natural-20261109-final` (private, ephemeral).
Historical publication and before-phase each passed once, without skips/retries.
The same dependent's UI-created interval is
`21c00b63-e48f-42ad-bfc6-cb056d848d15`, effective 10 November 2026; the previous
interval ends 9 November. Full interval and retained-history comparisons passed.
An actual early `finish` on 9 November refused before application traffic, leaving
the phase `awaiting-rollover`. At that checkpoint only labeled PostgreSQL container
`mycfc-338-natural-cbd3c9d76838` remained; no app/browser process was waiting.

### Owner decision: remaining checks skipped

The user subsequently requested skipping both the remaining manual accessibility
checks and the real date-boundary after phase. These checks are **not tested**,
not passed. The rollover and separate manual-test fixtures have been removed;
private evidence remains. The saved rehearsal can no longer be resumed; a later
attempt requires a new `begin` run and a new evidence directory. #338 acceptance
is not established by this prioritization decision.

The original finish invocation, retained here only as historical context, was:

```sh
python3 e2e/run-classification-natural-rollover.py finish /tmp/mycfc-338-natural-20261109-final
```

The cleanup invocation (now completed) was:

```sh
python3 e2e/run-classification-natural-rollover.py cleanup /tmp/mycfc-338-natural-20261109-final
```

The after phase was **skipped at the user's request**; #338 is not closed. A first exploratory
begin failed because axe required an explicitly created browser context. That
fixture was explicitly cleaned; the corrected spec passed on a new fresh fixture.
Seven offline phase/ownership/invariance tests and ESLint passed. Separately,
the field-error fix passed focused Go tests and all fourteen existing pinned
classification browser/axe cases (`/tmp/mycfc-338-fields-20261109-final`); these
results are not substituted for the skipped rollover result.
