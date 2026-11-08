#!/bin/sh
set -eu

# herdr-team-brief.sh
# Prints a role card and briefing into a terminal pane.

ROLE=${1:-lead}

# Real ANSI color escapes via printf
ESC=$(printf '\033')
C_RESET="${ESC}[0m"
C_BOLD="${ESC}[1m"
C_BLUE="${ESC}[1;34m"
C_CYAN="${ESC}[1;36m"
C_GREEN="${ESC}[1;32m"
C_YELLOW="${ESC}[1;33m"
C_MAGENTA="${ESC}[1;35m"
C_RED="${ESC}[1;31m"

clear 2>/dev/null || true

case "$ROLE" in
lead)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — DELIVERY LEAD & ORCHESTRATOR${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Delivery Lead & Technical Orchestrator
${C_CYAN}Tab:${C_RESET}            1: Delivery Lead & PO  |  ${C_CYAN}Pane:${C_RESET} lead
${C_GREEN}Permissions:${C_RESET}    Orchestration & contract integration (no direct self-approval)
${C_YELLOW}Primary Function:${C_RESET}
  • Coordinates overall delivery pipeline across specialist roles.
  • Manages vertical slicing and assigns non-overlapping files/worktrees.
  • Enforces human approval gates (Story Approval, Build Approval, Release Approval).
  • Reconciles contracts between frontend, backend, database, and devops.
${C_MAGENTA}Key References:${C_RESET}  AGENTS.md, docs/agent-team.md, docs/implementation-status.md
${C_MAGENTA}Quick Tools:${C_RESET}     herdr agent list | herdr worktree create | ./scripts/herdr-team-ctl.sh
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

product-owner)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — PRODUCT OWNER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Product Owner
${C_CYAN}Tab:${C_RESET}            1: Delivery Lead & PO  |  ${C_CYAN}Pane:${C_RESET} product-owner
${C_RED}Permissions:${C_RESET}    Read-Only (No code edits)
${C_YELLOW}Primary Function:${C_RESET}
  • Discovers user outcomes, problem statements, actors, and scope.
  • Produces reviewable Story Packets with observable acceptance criteria & non-goals.
  • Identifies policy, legal, or club decisions that require the human sponsor.
  • Halts for human story approval before creating GitHub issues or code implementation.
${C_MAGENTA}Key References:${C_RESET}  docs/ALL_SPECS.md, docs/implementation-status.md, AGENTS.md
${C_MAGENTA}Deliverable:${C_RESET}     Story Packet (Outcome, Actors, Scope, Non-Goals, Acceptance Criteria)
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

ux-researcher)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — UX RESEARCHER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           UX Researcher & Interaction Planner
${C_CYAN}Tab:${C_RESET}            2: Design & UX  |  ${C_CYAN}Pane:${C_RESET} ux-researcher
${C_RED}Permissions:${C_RESET}    Read-Only (No code edits)
${C_YELLOW}Primary Function:${C_RESET}
  • Maps actors, goals, user flows, and entry points in pt-PT member portal context.
  • Defines comprehensive state coverage: loading, empty, active, success, error.
  • Evaluates mobile ergonomics, progressive enhancement, and no-JavaScript fallbacks.
  • Flags usability risks and validates terminology (rowing/kayak-polo club domain).
${C_MAGENTA}Key References:${C_RESET}  docs/terminology.md, docs/design/, ui/views/
${C_MAGENTA}Deliverable:${C_RESET}     UX Brief, Journey Maps, State Matrix
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

product-designer)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — PRODUCT DESIGNER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Product Designer
${C_CYAN}Tab:${C_RESET}            2: Design & UX  |  ${C_CYAN}Pane:${C_RESET} product-designer
${C_RED}Permissions:${C_RESET}    Read-Only (No code edits)
${C_YELLOW}Primary Function:${C_RESET}
  • Translates approved UX journeys into responsive layouts and component specs.
  • Reuses design system tokens and preserves visual hierarchy across viewports.
  • Specifies forced-colour, high contrast, zoom, and reduced-motion states.
  • Coordinates with Frontend UI Engineer before new design primitives are introduced.
${C_MAGENTA}Key References:${C_RESET}  docs/design/foundation.md, docs/design/visual-direction.md
${C_MAGENTA}Deliverable:${C_RESET}     Design Brief, Component Specifications, Layout Blueprints
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

backend-domain)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — BACKEND DOMAIN ENGINEER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Backend Domain Engineer
${C_CYAN}Tab:${C_RESET}            3: Backend  |  ${C_CYAN}Pane:${C_RESET} backend-domain
${C_GREEN}Permissions:${C_RESET}    Workspace-Write (Go domain code in assigned branch/worktree)
${C_YELLOW}Primary Function:${C_RESET}
  • Implements Go domain behavior, business logic, handlers, and service boundaries.
  • Enforces authorization invariants, role checks, and guardian-minor relationships.
  • Ensures idempotency, transactions, and optimistic concurrency where required.
  • Writes focused Go unit tests (\`go test ./internal/<pkg> -run '...'\`).
${C_MAGENTA}Key Directories:${C_RESET} cmd/server/, internal/auth, internal/members, internal/events, etc.
${C_MAGENTA}Rule:${C_RESET}            Never hand-edit generated files. Coordinate schema with Data Engineer.
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

backend-data)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — BACKEND DATA ENGINEER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Backend Data Engineer
${C_CYAN}Tab:${C_RESET}            3: Backend  |  ${C_CYAN}Pane:${C_RESET} backend-data
${C_GREEN}Permissions:${C_RESET}    Workspace-Write (Schema, migrations, sqlc queries, storage)
${C_YELLOW}Primary Function:${C_RESET}
  • Maintains internal/db/schema.sql as the complete fresh-database baseline.
  • Writes forward-only, expand/contract-compatible migrations in internal/db/migrations/.
  • Authoritative queries in internal/db/queries/ — runs \`make generate\` for sqlc output.
  • Manages MinIO object storage boundaries and writes PostgreSQL/MinIO integration tests.
${C_MAGENTA}Key Commands:${C_RESET}    make generate | make test-integration | cmd/server migrate
${C_MAGENTA}Rule:${C_RESET}            Never edit internal/db/generated/*.go directly.
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

frontend-ui)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — FRONTEND UI ENGINEER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Frontend UI Engineer
${C_CYAN}Tab:${C_RESET}            4: Frontend  |  ${C_CYAN}Pane:${C_RESET} frontend-ui
${C_GREEN}Permissions:${C_RESET}    Workspace-Write (templ views, CSS, browser scripts)
${C_YELLOW}Primary Function:${C_RESET}
  • Implements server-rendered templ views in ui/views/.
  • Develops CSS/Tailwind styles and scripts in ui/static/src/.
  • Enforces progressive enhancement and ensures complete functionality without JavaScript.
  • Respects pt-PT copy conventions and design system component standards.
${C_MAGENTA}Key Commands:${C_RESET}    make generate (templ) | npm run build (bundles fingerprinted assets)
${C_MAGENTA}Rule:${C_RESET}            Never edit *_templ.go or ui/static/dist/ by hand.
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

frontend-a11y)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — FRONTEND ACCESSIBILITY ENGINEER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Frontend Accessibility Engineer
${C_CYAN}Tab:${C_RESET}            4: Frontend  |  ${C_CYAN}Pane:${C_RESET} frontend-a11y
${C_GREEN}Permissions:${C_RESET}    Workspace-Write (Accessibility semantics, Playwright/axe tests)
${C_YELLOW}Primary Function:${C_RESET}
  • Ensures strict semantic HTML, ARIA labels, keyboard focus traps, and tab order.
  • Authors Playwright and axe-core end-to-end accessibility test suites.
  • Tests responsive edges, zoom (200%), reduced motion, and forced-colour contrasts.
  • Validates manual accessibility scenarios in docs/accessibility-manual-check.md.
${C_MAGENTA}Key Commands:${C_RESET}    make test-e2e | npx playwright test
${C_MAGENTA}Rule:${C_RESET}            Never weaken axe findings or accessibility gates to make tests pass.
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

qa-engineer)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — QA ENGINEER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Independent QA Engineer
${C_CYAN}Tab:${C_RESET}            5: QA & Security  |  ${C_CYAN}Pane:${C_RESET} qa-engineer
${C_YELLOW}Permissions:${C_RESET}    Test code and fixtures only (No production code changes)
${C_YELLOW}Primary Function:${C_RESET}
  • Builds risk-based test matrix covering unit, integration, and e2e paths.
  • Independently reviews acceptance criteria vs final diff before approving verification.
  • Executes integration suites against real Dockerized PostgreSQL & MinIO.
  • Runs the repository verification gate (\`make verify\`). Reports exact defect findings.
${C_MAGENTA}Key Commands:${C_RESET}    make verify | make test-integration | make test-e2e
${C_MAGENTA}Rule:${C_RESET}            A failing test is evidence of a defect. Never modify tests to hide bugs.
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

security-reviewer)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — SECURITY REVIEWER${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Security & Privacy Reviewer
${C_CYAN}Tab:${C_RESET}            5: QA & Security  |  ${C_CYAN}Pane:${C_RESET} security-reviewer
${C_RED}Permissions:${C_RESET}    Read-Only (No code edits)
${C_YELLOW}Primary Function:${C_RESET}
  • Conducts evidence-based security and privacy audit on changes touching trust boundaries.
  • Audits authentication, sessions, CSRF, guardian authority over minors, and medical data.
  • Inspects file upload validation, MIME sniffing defenses, and private S3 buckets.
  • Reviews Terraform infrastructure, CI secrets, and dependency supply chain vulnerabilities.
${C_MAGENTA}Deliverable:${C_RESET}     Security Findings Report with Severity, Exploitation Path, & Mitigation
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

devops-sre)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — DEVOPS & SRE${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           DevOps & SRE
${C_CYAN}Tab:${C_RESET}            6: Ops & Runtime  |  ${C_CYAN}Pane:${C_RESET} devops-sre
${C_GREEN}Permissions:${C_RESET}    Workspace-Write (IaC, CI workflows, deployment scripts)
${C_YELLOW}Primary Function:${C_RESET}
  • Maintains Terraform configurations, Hetzner runtime, and AWS retainers.
  • Manages GitHub Actions workflows, immutable container builds, and staging environments.
  • Prepares rollback strategies, health check definitions, and backup procedures.
  • Verifies vendor version pinning and executes safe deployment rehearsal tooling.
${C_MAGENTA}Key Files:${C_RESET}       deploy/, docker-compose.yml, .github/workflows/, scripts/release.sh
${C_RED}Safety Rule:${C_RESET}     NEVER touch live production, secrets, or DNS without explicit human authorization.
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

runtime-console)
printf '%b' "\
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_BOLD}  MYCFC SOFTWARE DEVELOPMENT TEAM — RUNTIME CONSOLE${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
${C_CYAN}Role:${C_RESET}           Local Runtime & Dev Server Console
${C_CYAN}Tab:${C_RESET}            6: Ops & Runtime  |  ${C_CYAN}Pane:${C_RESET} runtime-console
${C_GREEN}Function:${C_RESET}       Live server execution, database migrations, and active logging.
${C_YELLOW}Helpful Commands:${C_RESET}
  • Start live reload dev server: ${C_BOLD}PATH=\"\$PWD/bin:\$PATH\" make dev${C_RESET}
  • Rebuild local bootstrap:      ${C_BOLD}make dev-bootstrap${C_RESET}
  • Run database migrations:      ${C_BOLD}go run ./cmd/server migrate${C_RESET}
  • Run integration tests:        ${C_BOLD}make test-integration${C_RESET}
  • Run full verification:        ${C_BOLD}make verify${C_RESET}
${C_BLUE}══════════════════════════════════════════════════════════════════════════════${C_RESET}
"
;;

*)
  echo "Unknown role: $ROLE"
  exit 1
  ;;
esac
