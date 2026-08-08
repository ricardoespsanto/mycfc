# MyCFC production implementation specification

This directory replaces the original short prompts with one canonical, internally consistent implementation contract intended for autonomous implementation by an LLM coding agent.

## Execution order

The agent MUST process the documents in numeric order and treat earlier global decisions as binding:

1. `00_system_context.md`
2. `01_data_models.md`
3. `02_routing_and_middleware.md`
4. `03_htmx_dashboards.md`
5. `04_equipment_workflow.md`
6. `05_auth_and_consent.md`
7. `06_frontend_a11y_pt_PT.md`
8. `07_aws_deployment_gitops.md`
9. `08_github_actions_pipeline.md`
10. `09_local_dev_and_minio.md`
11. `10_acceptance_test_matrix.md`

`arch_diagram.svg` is the normative deployment overview. The Markdown documents remain authoritative when a visual detail is unclear.

## Meaning of “unattended implementation ready”

The implementation agent MUST NOT ask the operator to choose architecture, packages, route behaviour, database constraints, error semantics, deployment order, or test strategy. Those decisions are fixed here.

The operator still has to supply external account-specific values that cannot safely be invented: AWS account/region, Route 53 hosted-zone ID, GitHub organisation and repository, production domain, gallery URL, and current legal-document versions/hashes. Terraform variables and environment validation MUST fail clearly when one is absent.

## Deliberate architecture correction

The production target is **Amazon ECS on Fargate behind an Application Load Balancer**, not AWS App Runner. App Runner stopped accepting new customers on 31 March 2026, so it is not a valid default for a deployable new production environment.

## Completion rule

The repository is complete only when every mandatory command and scenario in `10_acceptance_test_matrix.md` passes and there are no unresolved `TODO`, `FIXME`, dummy secrets, wildcard IAM resources that are not explicitly permitted, or placeholder runtime values.
