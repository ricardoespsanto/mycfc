# Wireframe Implementation Plan

This plan turns `docs/mycfc wireframes.png` into incremental delivery work without replacing the numbered specifications in `docs/ALL_SPECS.md`. The specifications remain authoritative for architecture, routes, security, and acceptance behaviour; the wireframe guides layout and information hierarchy.

## Delivery Rules

- Complete one checkpoint at a time and pause for operator build and testing before starting the next.
- Keep `docs/implementation-status.md` current when a checkpoint changes a specification's state.
- End each checkpoint with focused tests and the strongest currently green repository gate.
- Do not add controls that lead to placeholders. A visible action must work for both normal forms and HTMX where the specification requires both.
- Hold wireframe-only features outside the existing route and data contracts until their product behaviour is agreed.

## Scope Boundary

The committed MVP includes login, adult registration, role-based dashboards, guardian dependants, repair reporting, administrator fleet visibility and maintenance scheduling. The following wireframe concepts are deferred scope because the current specifications do not define their routes or data contracts:

- athlete rankings and server-derived upcoming-event summaries;
- guardian payment tracking, activity history, and targeted news;
- profile, search, member-management, and news-authoring pages;
- repair-status and maintenance-completion administration;
- rich news images, editable announcements, and editable club information.

These items must not appear as active dead-end controls in the MVP.

## Checkpoints

### 0. Stabilise the Foundation

Existing phases: 02 Routing and middleware, 09 Local development, 10 Acceptance matrix.

- Fix the router test harness so `make test` is a reliable baseline.
- Reconcile `.env.example`, Compose, bootstrap, MinIO, and test-database settings.
- Make Air use repository-local tools and watch the sources required by the specification.
- Verify a fresh local bootstrap, migrations, asset build, focused tests, and `make verify-foundation`.

Operator checkpoint: a fresh local environment starts and the current foundation tests pass.

### 1. Authentication Vertical Slice

Existing phases: 02 Routing and middleware, 05 Authentication and consent, 06 Accessibility/localisation.

- Add shared templ page, navigation, form, flash, and error components.
- Implement typed login and registration pages in pt-PT.
- Implement adult registration with bcrypt and transactional consent records.
- Implement timing-safe login, session renewal, safe `next`, and POST logout.
- Load current users from PostgreSQL and add anonymous, authenticated, and database-authoritative role guards.
- Implement `/dashboard` role redirects and rendered 403/404/500 responses.

Operator checkpoint: each public role can register, authenticate, reach only its destination, and log out with and without JavaScript.

### 2. Shared Dashboard and Calendar Shell

Existing phases: 03 Dashboards, 06 Accessibility/localisation.

- Replace the asset copier with the pinned bundled frontend toolchain specified in phase 03.
- Add typed page/dashboard view models; templates never receive sqlc structs.
- Build the responsive wireframe shell, role navigation, cards, tables, status badges, and empty states.
- Add manifest-resolved local CSS/JS with no inline or CDN assets.
- Implement accessible public Google Calendar enhancement, role source mapping, deduplication, and no-JavaScript links.
- Apply five-second dashboard query deadlines and fail complete pages on required-query errors.

Operator checkpoint: every role receives a responsive empty-state shell with correct navigation and calendar sources.

### 3. Competitor Dashboard

Existing phases: 03 Dashboards, 04 Repair workflow.

- Render user summary, recent performance metrics, training logs, relevant WhatsApp groups, and training/competition calendars.
- Add the shared repair form when the repair flow is available; do not render a dead submit action beforehand.
- Cover populated and empty states and prevent cross-role data/navigation leakage.

Operator checkpoint: a competitor sees only their database-backed data and relevant external links.

### 4. Leisure Dashboard

Existing phases: 03 Dashboards, 04 Repair workflow.

- Render published news, gallery link, relevant WhatsApp groups, and social/clean-up calendars.
- Add the shared repair form when functional.
- Keep wireframe-only rich media and announcements out of the UI until separately specified.

Operator checkpoint: a leisure member can browse current configured content and complete core navigation without JavaScript.

### 5. Guardian and Dependants

Existing phases: 03 Dashboards, 05 Authentication and consent.

- Render all active dependants with role, squad, age, and schedule-source labels.
- Build combined, deduplicated calendar sources from dependant roles.
- Implement the add-dependant form and transactional responsibility consent.
- Enforce role, age, squad, ownership, and ten-active-dependant rules from the database user, never browser identity fields.
- Return complete normal-form redirects and accessible HTMX success/error replacements.

Operator checkpoint: a guardian can add and view dependants, while another role or spoofed identity cannot.

### 6. Repair Reporting

Existing phases: 04 Repair workflow, 10 Acceptance matrix.

- Implement the multipart repair component for active, non-retired equipment.
- Enforce request limits, explicit fields, UUIDs, descriptions, and authoritative image validation.
- Implement same-user idempotency, cross-user collision handling, private upload, database insertion, and storage compensation.
- Return accessible HTMX replacements and normal-form redirects.
- Test no-photo, valid formats, hostile files, retries, concurrent submissions, and failed storage/database paths.

Operator checkpoint: every active adult role can submit an idempotent repair with an optional private photo against local MinIO.

### 7. Administrator Fleet and Maintenance

Existing phases: 03 Dashboards, 04 Repair workflow.

- Render equipment status counts, capped inventory, pending repairs, and upcoming maintenance.
- Generate short-lived repair-photo URLs only for displayed administrator rows.
- Add the missing equipment-status query required by maintenance scheduling.
- Implement transactional maintenance scheduling with HTMX and normal-form responses.
- Provide accessible responsive alternatives for fleet tables.

Operator checkpoint: an administrator can inspect fleet/repair state and schedule maintenance; non-admin users cannot access either operation.

### 8. Acceptance and Production Delivery

Existing phases: 06 Accessibility/localisation, 07 AWS deployment, 08 GitHub Actions, 10 Acceptance matrix.

- Add browser flows with JavaScript enabled and disabled, axe checks, keyboard checks, and responsive/zoom coverage as each page stabilises.
- Complete integration coverage for PostgreSQL and MinIO.
- Implement production Terraform only after the application gate is green.
- Implement CI and deployment workflows only after their invoked commands and infrastructure exist.
- Run and record the complete phase 10 acceptance matrix before declaring the application complete.

Operator checkpoint: application, infrastructure, and delivery gates satisfy the existing release contract.

## Data Provisioning Decision

Equipment, training logs, performance metrics, news, and WhatsApp groups have read models but no specified authoring UI. Before populated-dashboard acceptance, choose and specify one operational source: administrator routes, CLI/import commands, or an external owner. Until then, tests may create fixtures directly and production UI must show honest empty states.
