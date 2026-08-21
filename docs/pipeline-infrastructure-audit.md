# Pipeline and infrastructure audit

Audit date: 2026-08-12. Scores describe completion visible in this repository
after the accompanying CI-quality changes: 1 means effectively absent; 10 means
complete, continuously verified, and operationally evidenced. They are not a
security certification.

## Executive assessment

The repository has a strong delivery foundation: exact tool and provider
versions, SHA-pinned GitHub Actions, minimal workflow permissions, GitHub OIDC
for AWS publication, immutable ECR releases, blue-green host promotion,
least-privilege runtime identities, private networking, encrypted backups, and
documented rollback/restore procedures. The weighted overall completion is
**7/10**.

The largest remaining risks are not basic CI correctness. They are depth of
unit coverage, software-supply-chain evidence, long-lived host AWS credentials,
container/host resource hardening, and operational proof that backups and
alerts continue to work. GitHub branch-protection rules, production-environment
reviewers, Cloudflare account controls, live alarm subscriptions, and completed
restore-drill records are outside this repository and were not assumed to
exist.

## Scorecard

| Aspect | Completion | Evidence in the repository | What is still missing |
| --- | ---: | --- | --- |
| CI correctness and reproducibility | 8/10 | Parallel generated, foundation, quality, integration, and E2E gates; pinned Ubuntu, Go and Node versions; concurrency cancellation; explicit aggregate result gate | Confirm the aggregate and Infrastructure checks are required by branch protection; add a scheduled full-pipeline run to catch external image/service drift |
| Static analysis and formatting | 8/10 | gofmt, vet, Staticcheck, ESLint, Stylelint, HTMX/templ checks, ShellCheck, actionlint, Hadolint, Terraform fmt/validate and TFLint | Remove the documented Staticcheck CSRF/package-comment exceptions over time; consider SQL linting and a rendered-HTML conformance checker if their signal justifies maintenance |
| Unit tests and coverage | 8/10 | Atomic Go profile, text and browsable HTML reports, per-file and changed-executable-line evidence, 14-day CI artifact, GitHub summary, UI Go tests, and overall plus per-package regression floors over hand-written source | The primary hand-written Go floor is 85%; maintain risk-focused behavioural coverage and add JavaScript unit tests where they provide useful signal |
| Integration and browser assurance | 8/10 | PostgreSQL/MinIO integration suites, deployment-script tests, Playwright flows, and axe accessibility coverage run as separate gates | Coverage from integration tests is not merged into the report; only the pinned Chromium environment is evidenced; add selected failure/upgrade-path tests rather than broad browser duplication |
| Workflow trust boundaries | 9/10 | Read-only default permissions, deployment-only OIDC, production environment, tested-SHA verification, immutable digest lookup, and non-cancelling deploy concurrency | Verify production environment reviewers/rules in GitHub settings; consider a dedicated reusable build workflow to remove any residual divergence between tested and published builds |
| Dependency and supply-chain security | 6/10 | Dependabot covers Actions, Go, npm and Docker; direct dependencies are exact; Actions use commit SHAs; ECR tags are immutable and scan on push | Add `govulncheck`, npm audit, filesystem/container scanning, SBOM generation, provenance/attestation, and image signing with a deployment verification policy |
| Release safety and rollback | 8/10 | Build from a verified successful main SHA; publish by digest; blue-green candidate checks; atomic Caddy switch; failed-release quarantine; previous slot retained | GitHub does not receive confirmation that host pickup succeeded; automate rollback exercises and expose release age/failed pickup as a first-class alert |
| Terraform and state management | 9/10 | All three roots are now formatted, initialized without backends, validated and linted; providers and Terraform are exact and locked; production and Hetzner state use versioned, encrypted S3 storage with native locking; the Hetzner state migration was completed and payload-verified on 2026-08-12 | Add reviewed plans for trusted changes and policy/security scanning; consider customer-managed KMS state encryption and tightly audit backend access |
| Runtime and host hardening | 6/10 | Distroless non-root app, closed inbound web ports, allowlisted SSH, Cloudflare Tunnel, private Compose network, health checks, and sandboxed systemd units | Add Compose CPU/memory/PID limits, read-only filesystems and capability drops where compatible; codify host bootstrap/security updates; pin every production build/base image by digest |
| Secrets and workload identity | 6/10 | Runtime secrets in Secrets Manager/SSM, root-owned mode-0600 files, separated least-privilege app/release/backup identities, GitHub AWS OIDC | Hetzner uses long-lived IAM access keys and a tunnel token in container environment; document/enforce rotation and investigate short-lived credentials (for example IAM Roles Anywhere) without widening permissions; sensitive Terraform values remain in state |
| Observability and incident response | 6/10 | Health endpoints, deployment journal/CloudWatch forwarding, deployment failure metric/alarm, release-status command, privacy-safe password-recovery operations | Add application request/error/latency metrics, structured dashboards/SLOs, uptime checks, database/disk/certificate alerts, and direct backup-job failure/age alerts |
| Backup and disaster recovery | 7/10 | Nightly encrypted logical dumps, daily/monthly retention, KMS envelope encryption, Hetzner backups, checksum verification, stated RPO/RTO, isolated restore drill | Restore drills are manual and evidence lives outside the repo; automate backup-age/failure monitoring and periodic restore verification, and rehearse full replacement-host recovery |
| Security and operational documentation | 8/10 | Detailed deployment, rollback, backup, password-recovery and architecture guidance; supported release line is current | Verify the published security mailbox and response ownership; add an incident runbook with named escalation/communications roles and record external control evidence |

## Changes made in this audit

- Added one CI quality job for application, HTMX/templ, CSS, shell, workflow,
  Dockerfile, and Terraform linting.
- Added a deterministic unit-coverage gate and downloadable text, profile, and
  HTML reports. Generated sqlc and templ Go wrappers are excluded; their
  behavior remains covered by integration, rendering, and browser gates.
- Ratcheted the hand-written-Go unit-coverage floor to 85.0%. The same filtered
  atomic profile is used locally and in CI; CI fetches the comparison commit and
  records aggregate, package, file, largest-uncovered and changed-executable-line
  evidence. Every changed hand-written Go source line must meet the 85% coverage
  expectation. Integration coverage remains a separate gate and is never merged
  into this unit denominator.
- Fixed defects exposed by the new linters: a discarded login value, dead Go
  helpers, shell portability/quoting issues, and retired Terraform variables.
- Made Terraform validation cover `bootstrap`, `hetzner`, and `production`, and
  made the Infrastructure workflow trigger for every Terraform root.
- Configured the Hetzner stack to use the encrypted, versioned S3 backend. The
  existing 19-resource state was migrated on 2026-08-12 with timestamped local
  backups; resources, instances, outputs, and validation checks were verified
  against the remote copy. This remains an operator-run operation, not a CI
  responsibility.
- Replaced the aggregate CI job's order-dependent result comparison with
  explicit checks, and removed a duplicate full unit-test invocation.

## Recommended next increments

1. Maintain the 85% hand-written-Go floor with focused behavioural tests for
   identity, event/communication, training, and operational handlers; raise
   package floors only when their measured baseline supports the ratchet.
2. Add vulnerability, secret, IaC-security and container scanning with pinned
   tools, followed by SBOM/provenance generation and signed-image verification.
3. Harden production Compose services with measured resource limits,
   capabilities, and read-only filesystems, validating backup/migration paths
   before rollout.
4. Add backup-age/failure and application SLO alerts, then record automated or
   scheduled restore and rollback evidence.
5. Verify repository settings: required checks, approval rules, signed/linear
   merge policy, production reviewers, secret scanning/push protection, and
   private vulnerability reporting.
