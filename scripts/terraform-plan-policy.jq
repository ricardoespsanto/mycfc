def permitted_resource_actions:
  . == ["no-op"] or
  . == ["create"] or
  . == ["read"] or
  . == ["update"];

.format_version == "1.2" and
.errored == false and
.complete == true and
(.configuration.root_module | type == "object") and
((.deferred_changes // []) | length == 0) and
((.action_invocations // []) | length == 0) and
((.deferred_action_invocations // []) | length == 0) and
all(.resource_changes[]?;
  (.change.importing? == null) and
  (.change.actions | permitted_resource_actions)
) and
([
  .configuration.root_module |
  .. |
  objects |
  (.provisioners? // empty) |
  .[]?
] | length == 0)
