# MyCFC agent team

The human remains product sponsor and release authority. The primary Codex task acts as delivery lead: it keeps the shared context, invokes specialist agents for bounded work, reconciles their outputs, and returns decisions for human approval.

## Roster

| Role | Agent | Primary responsibility | May write code? |
|---|---|---|---|
| Product owner | `product_owner` | Discovery, scope, stories, acceptance criteria, prioritization questions | No |
| UX researcher | `ux_researcher` | Journeys, information architecture, usability, state coverage | No |
| Product designer | `product_designer` | Visual hierarchy, responsive specifications, design-system coherence | No |
| Frontend engineer | `frontend_ui_engineer` | templ UI, CSS, progressive enhancement, integration | Yes |
| Accessibility frontend engineer | `frontend_accessibility_engineer` | Semantics, keyboard/focus, responsive edges, Playwright/axe | Yes |
| Backend domain engineer | `backend_domain_engineer` | Go behavior, handlers, authorization, transactions | Yes |
| Backend data engineer | `backend_data_engineer` | PostgreSQL, migrations, sqlc, MinIO, integration tests | Yes |
| DevOps/SRE | `devops_sre` | Terraform, Hetzner, AWS remnants, CI/CD, observability, releases | Yes |
| QA engineer | `qa_engineer` | Risk-based test plans, test implementation, independent review | Tests only by default |
| Security reviewer | `security_reviewer` | Targeted security and privacy review | No |

The delivery lead/technical lead is intentionally the primary task rather than another subagent. It owns contracts and integration but has no authority to approve product scope, merge, or deploy.

## Operating model

`AGENTS.md` loads `$mycfc-delivery` as the default workflow for every new MyCFC task. Codex applies only the phases relevant to the request, so simple questions do not spawn the full team. A normal feature moves through:

1. Product discovery and clarification.
2. UX/design and technical feasibility, using at most three parallel specialists.
3. A single story packet for human approval.
4. GitHub issue creation only after approval.
5. Human selection of an issue for implementation.
6. One writer per checkout; use separate worktrees for genuinely independent slices.
7. Independent QA and conditional security review.
8. Human review and explicit merge/release approval.

Agents are not persistent employees and do not share memory automatically. GitHub issues, pull requests, repository documentation, and committed code are the durable source of truth. Subagents are temporary executions of these roles.

## Useful prompts

Discovery:

```text
Use $mycfc-delivery to refine this idea. Have the product owner lead discovery, involve UX and design where useful, and stop with the questions and draft story packet before creating any GitHub issue: <idea>
```

Implementation:

```text
Use $mycfc-delivery to implement approved issue #<number>. Delegate bounded frontend, backend, and QA work where it is independent. Do not merge or deploy; return the verified diff for my approval.
```

Review:

```text
Use $mycfc-delivery to review this branch against its issue. Have QA review behavior and tests, and add a security review if the change crosses a security or privacy boundary. Do not edit until I select findings to fix.
```

## Scaling the team

Do not activate every role for every story. Add temporary specialties only when evidence demands them—for example content/localisation, data migration, performance, or club-domain subject-matter review. The human club-domain expert is especially important where rowing/kayak-polo terminology, safeguarding, membership policy, or operational practice cannot be inferred from code.
