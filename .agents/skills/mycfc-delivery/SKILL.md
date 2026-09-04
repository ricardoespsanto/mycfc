---
name: mycfc-delivery
description: Coordinate MyCFC product discovery, user-story refinement, UX and design planning, implementation delegation, QA and security review, and approval-gated GitHub delivery. Use when an idea, bug, feature, epic, GitHub issue, or approved story needs to move through the MyCFC team workflow.
---

# MyCFC Delivery

Keep the primary agent as delivery lead and the human as product authority. Use custom agents for bounded specialist work; never simulate an autonomous organization that makes product or release decisions without the human.

## Establish the working state

1. Read `AGENTS.md` and `docs/implementation-status.md`.
2. Inspect `git status` and preserve unrelated or in-progress changes.
3. Identify whether the request is discovery, refinement, implementation, review, or release work.
4. Use GitHub as the durable work record only after the human authorizes the relevant write.

## Discover and refine

1. Ask `product_owner` to identify the outcome, actors, decisions, scope, non-goals, dependencies, and acceptance criteria.
2. For user-facing work, run `ux_researcher` and `product_designer` in parallel after they receive the same product question and relevant evidence.
3. Add `backend_domain_engineer` or `backend_data_engineer` in read/analysis mode when feasibility, authorization, or persistence shapes the story.
4. Add `devops_sre` when delivery, infrastructure, configuration, observability, or operational ownership is involved.
5. Add `security_reviewer` when the work crosses a security or privacy trigger described by that agent.
6. Have the primary agent reconcile conflicts and return only genuine product decisions to the human.

Do not run more than three subagents concurrently. Prefer parallel read-heavy work. Do not let multiple agents write the same checkout.

## Produce the story packet

Return one reviewable packet containing:

- title and user outcome;
- actor or persona and problem statement;
- scope and explicit non-goals;
- acceptance criteria expressed as observable behavior;
- UX states and responsive/accessibility requirements when applicable;
- authorization, privacy, data, migration, operational, and rollout constraints;
- dependencies and open decisions;
- test plan and required evidence;
- suggested labels and implementation slices without assuming labels exist.

Mark assumptions explicitly. Never invent club policy, permissions, legal requirements, production facts, or dates.

## Enforce human gates

Pause for the human at these boundaries:

1. **Story approval:** before creating or materially updating a GitHub issue.
2. **Build approval:** before beginning implementation of a refined story unless the human already requested implementation.
3. **Release approval:** before merge, deployment, live migration, secret changes, DNS changes, or other production actions.

Human approval of one boundary does not imply approval of the next. Drafting an issue or PR body is read-only; publishing it is a write.

## Implement approved work

1. Start from an approved issue or story packet and restate file ownership and contracts.
2. Use one writer per checkout. For parallel implementation, give each independent slice its own branch or Codex worktree and avoid overlapping files.
3. Route UI work to `frontend_ui_engineer`; route accessibility and browser resilience to `frontend_accessibility_engineer`.
4. Route Go behavior to `backend_domain_engineer`; route schema, migrations, sqlc, PostgreSQL, or MinIO work to `backend_data_engineer`.
5. Route infrastructure, CI/CD, deployment, observability, backup, or release work to `devops_sre`.
6. Have the primary agent integrate contracts and resolve conflicts. Implement the smallest complete vertical slice; do not silently absorb follow-up scope.

## Verify independently

1. Ask `qa_engineer` for a risk-based review of the acceptance criteria and final diff. Let QA add missing tests only after reporting the gap and with non-overlapping ownership.
2. Ask `security_reviewer` for an independent read-only pass when a trigger applies.
3. Run focused tests during development and the proportionate repository gate before handoff. Use `make verify` for a release-ready full gate when the environment supports it.
4. Treat failed checks as findings. Do not weaken gates or rewrite acceptance criteria to declare success.
5. Update implementation-status and acceptance evidence only when behavior genuinely changed.

## Hand off

Summarize the delivered outcome, issue or PR linkage, changed files, verification evidence, known gaps, operational steps, and decisions still owned by the human. Never merge or deploy unless the human explicitly asks for that exact action.

