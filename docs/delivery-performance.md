# Delivery performance evidence

This document is the durable evidence plan for epic #184. Source changes do not
by themselves satisfy the empirical or live-operation acceptance criteria. Run
all trials from one tested commit and retain the linked GitHub Actions runs or
sanitized production observations before closing a child issue.

## Playwright worker scaling (#180)

CI remains at two workers unless a candidate proves both stability and a browser
execution median at least 20% below the contemporaneous two-worker median. The
trial harness accepts only two, three, or four workers and records Playwright's
machine-readable browser duration, the tested SHA, runner, test-inventory hash,
retries, outcome counts, JSON report, log, and failure artifacts. Each sample
uses a separate Compose project and removes only that isolated project's volumes.
The harness refuses a dirty checkout so its recorded SHA identifies the complete
tested source; `E2E_TRIAL_ALLOW_DIRTY=true` is only for local harness smoke tests
and its output is not acceptance evidence.
Trials reject non-local Docker daemons by default. The bootstrap preserves the
caller's Compose project, Docker host/context, and Compose file/profile/env-file
controls even if the ignored `.env` contains conflicting values; every bootstrap
command also names the repository's two Compose files explicitly.

Apply the maintainer-controlled `e2e-worker-trial` label to a pull request, or
run the manual `E2E worker trial` workflow with its default 20 samples after the
workflow is on the default branch. It runs all three worker counts sequentially
on one representative GitHub-hosted runner and retains the TSV, JSON, logs, and
failure traces as a 30-day artifact. The same harness can be exercised locally
with:

```sh
E2E_WORKERS=2 E2E_RUNS=20 make test-e2e-workers
E2E_WORKERS=3 E2E_RUNS=20 make test-e2e-workers
E2E_WORKERS=4 E2E_RUNS=20 make test-e2e-workers
```

Every accepted row must show 51 discovered tests, 20 passed tests, 31 existing
UI-review skips, zero unexpected or flaky results, and zero retries. A failure
breaks the consecutive-success window; retain its log, JSON, and traces and
replace the harness's initial failure class with the reviewed classification.
Do not use Playwright `--repeat-each`: the suite contains deliberately ordered,
stateful journeys.

As of 2026-08-22, the worker override and evidence harness exist, but the
required 20-run comparison has not been performed. Two workers therefore remain
the default and no scaling result is accepted yet.

## Release pickup latency (#181)

The repository timer now uses a 30-second interval, 10 seconds of jitter, and
one-second timer accuracy. Its scheduling bound is 41 seconds. The release agent
persists a tag-and-digest-associated, first-observation timeline for the release
tag timestamp (a conservative publication-start proxy), agent start, ECR
detection, verified image pull, migration completion, candidate
readiness, traffic switch, and deployment completion. `release-status.sh`
exposes those timestamps and the publication-to-start, detection, switch, and
completion durations without reading or printing application credentials.
The deployment workflow separately records the authoritative post-promotion
`published_at` timestamp in its GitHub summary. Correlate that value with the
same release tag and digest when calculating accepted pickup latency; host-only
durations deliberately include the promotion interval and therefore overstate,
rather than understate, pickup time.

Installing or restarting this timer on production is a separate release gate.
After explicit approval, use an agreed observation window and record:

- at least one observed release, followed by enough releases to calculate the
  publication-to-agent-start p90;
- `systemctl list-timers mycfc-pull-release.timer` and the sanitized
  `release-status.sh` output for each observation;
- ECR request volume from CloudTrail or equivalent AWS metrics before and after;
- timer/service activations and host CPU/load from the system journal and host
  monitoring;
- `/mycfc/production/deployment` `IncomingBytes`, stored bytes, and event count
  before and after the polling change.

Accept the rollout only if pickup p90 is at most 60 seconds and the observed API,
host-load, wakeup, and log-volume costs are acceptable. Roll back by reinstalling
the prior timer, running `systemctl daemon-reload`, and restarting the timer; the
release agent's digest verification, quarantine, migration, blue-green checks,
traffic switch, and rollback semantics do not change.

As of 2026-08-22, the source and tests are present, but no production timer
change or live observation has been authorized or claimed.

## Documentation-only pull requests (#182)

The conservative documentation-only allowlist is:

- `README.md`;
- `SECURITY.md`;
- Markdown files below `docs/`.

Everything else, including `AGENTS.md` at any depth, `.agents/`, generated diagrams and
images, HTML prototypes, source, configuration, dependencies, workflows,
scripts, Docker, Terraform, and generated application assets takes the full CI
path. Empty changes, missing refs, incomplete history, diff failures, unusual
or ambiguous paths, and every push to `main` also take the full path. Rename
detection is disabled so a code-to-documentation rename exposes both the code
deletion and documentation addition and therefore runs full CI.

The aggregate `summary` job remains the sole check intended for branch
protection. Its accepted result matrix is:

| Mode | Classifier | Documentation | Five full gates | Summary |
| --- | --- | --- | --- | --- |
| Documentation PR | success / `docs` | success | all skipped | success |
| Full PR or every main push | success / `full` | skipped | all success | success |
| Classifier failure | failed | skipped | all still run and must succeed | failed |

The classifier's repository-backed tests cover documentation, mixed changes,
dependencies, workflows, Docker, Terraform, scripts, generated artifacts,
renames, deletions, empty diffs, invalid refs, unusual filenames, and main-push
fallback.

For pull requests, the workflow executes the classifier from a separate checkout
of the exact reviewed base SHA; a PR cannot replace the classifier it is asking
CI to trust. If that trusted classifier is absent or unavailable, CI runs the
full gate. `CODEOWNERS` assigns the classifier, every workflow, and the ownership
file to the repository owner. The ruleset must require code-owner review for those
paths as well as the aggregate status check before the route is adopted.

The repository's `main` branch had no protection rule requiring `summary` when
checked on 2026-08-22. Adoption therefore requires a separately authorized
repository-ruleset change that requires only the `summary` check from GitHub
Actions and requires code-owner review for protected CI paths, followed by one
observed documentation-only PR and one mixed/code PR.
Do not require conditional jobs individually because their intentional skipped
state would make the other mode unmergeable.

The mixed/code half of that observation is recorded by [PR #221 CI attempt
32580194688](https://github.com/ricardoespsanto/mycfc/actions/runs/32580194688):
classification and every full gate succeeded, documentation was skipped, and
`summary` succeeded. Because the trusted classifier is not yet present on the
base branch, the required documentation-only observation remains post-merge.

## Terraform provider cache (#183)

The Infrastructure workflow caches only Terraform's shared provider plugin
directory. Its exact key includes runner OS and architecture, the Terraform
version resolved from the Makefile and all three dependency lock files. There
are no broad restore keys. Backend-disabled initialization
keeps separate `TF_DATA_DIR` values for bootstrap, Hetzner, and production; the
cache cannot contain state, plans, backend configuration, or credentials.

Evidence requires a successful empty-cache `make terraform-check`, a second
successful run that reuses provider binaries, `make lint-workflows`, and linked
GitHub cold/warm logs. A separate Terraform-version change and lock-file change
must each demonstrate a miss. Compare the warm timing with the seven-run
pre-change Terraform-step baseline of 49, 49, 49, 47, 44, 49, and 55 seconds
(median 49 seconds), and retain the cache only if the improvement is material
without reduced reliability.

Hosted evidence on 2026-08-22 satisfies the cache experiment:

- [cold attempt 1](https://github.com/ricardoespsanto/mycfc/actions/runs/32580194638/attempts/1)
  missed the exact key, passed every root in 35 seconds, and saved a 193,055,402
  byte cache;
- [warm attempt 2](https://github.com/ricardoespsanto/mycfc/actions/runs/32580194638/attempts/2)
  restored that exact key and passed in 28 seconds, 43% below the 49-second
  baseline despite four seconds of restore overhead;
- a [lock-file change](https://github.com/ricardoespsanto/mycfc/actions/runs/32580481258)
  and a [Terraform-version change](https://github.com/ricardoespsanto/mycfc/actions/runs/32580570325)
  each produced a distinct confirmed miss and passed every root.

A local empty-cache check completed in 27 seconds and the immediate warm check
in 12 seconds. Inspection found only provider binaries and their vendor
documentation/licenses—no state, plan, backend, variable, or credential files.
The cache therefore meets #183's correctness, invalidation, boundary, and
material-improvement criteria; adoption remains contingent only on merging the
reviewed source.

## Epic aggregate (#184)

After all P0 and accepted P1/P2 source changes are present, collect five
representative successful CI and image-publication runs. Record elapsed
critical-path time and summed job duration separately. Epic acceptance requires
a full-CI median at or below 145 seconds, p90 at or below 180 seconds, at least
20% fewer runner-minutes, and warm image publication median at or below 55
seconds, without removed tests, new skips, weakened required checks, or changed
deployment safety semantics.
