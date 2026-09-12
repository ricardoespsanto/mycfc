# MyCFC architecture diagrams

The architecture is split into two normative views so runtime behaviour is not confused with delivery orchestration.

## Production runtime

![MyCFC production runtime architecture](architecture_runtime.svg)

`architecture_runtime.svg` defines:

- Internet-facing and private trust boundaries.
- Browser, DNS, WAF, TLS and ALB request flow.
- ECS application tasks, VPC endpoints and isolated RDS access.
- Private S3 repair-image access.
- Runtime secrets, image pulls, logging, metrics and alerting.

It intentionally excludes GitHub Actions, Terraform state, image builds and migration deployment steps.

## CI/CD and deployment

![MyCFC delivery pipeline](architecture_delivery_pipeline.svg)

`architecture_delivery_pipeline.svg` defines:

- Required source checks plus the predecessor-to-candidate production release gate.
- Separate human gates for merge, infrastructure, publication/deployment, and activation.
- Signed semantic-tag and protected manual-dispatch release initiation.
- Immutable ECR image, provenance, schema digest, and canonical publication evidence.
- Pull-based Hetzner blue-green promotion with ordered database/guardian phases.
- Direct candidate checks, atomic Caddy switching, no-switch failure, rollback, and quarantine.
- A sanitized atomic host receipt verified through a keyless, read-only AWS observer role.
- GitHub release and explicit issue updates only after exact production evidence is verified.

The host has no GitHub credential, and the observer cannot read secrets/state or mutate AWS resources.

## Editing and regeneration

The `.dot` files are canonical. Regenerate generated images with Graphviz:

```bash
dot -Tsvg architecture_runtime.dot -o architecture_runtime.svg
dot -Tsvg architecture_delivery_pipeline.dot -o architecture_delivery_pipeline.svg

dot -Tpng -Gdpi=130 architecture_runtime.dot -o architecture_runtime_preview.png
dot -Tpng -Gdpi=130 architecture_delivery_pipeline.dot -o architecture_delivery_pipeline_preview.png
```

Do not manually edit the generated SVG markup. The numbered Markdown specifications remain authoritative if a diagram label is necessarily abbreviated.
