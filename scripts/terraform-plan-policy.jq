def permitted_resource_actions:
  . == ["no-op"] or
  . == ["create"] or
  . == ["read"] or
  . == ["update"];

def permitted_resource_change:
  (.change.actions | permitted_resource_actions) or
  (
    .address == "aws_ecr_lifecycle_policy.app" and
    .type == "aws_ecr_lifecycle_policy" and
    .change.actions == ["delete", "create"]
  );

.format_version == "1.2" and
.errored == false and
.complete == true and
(.configuration.root_module | type == "object") and
((.deferred_changes // []) | length == 0) and
((.action_invocations // []) | length == 0) and
((.deferred_action_invocations // []) | length == 0) and
all(.resource_changes[]?;
  (.change.importing? == null) and
  permitted_resource_change
) and
([
  .configuration.root_module |
  .. |
  objects |
  (.provisioners? // empty) |
  .[]?
] | length == 0)
