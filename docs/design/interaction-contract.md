# Interaction and form contract

Status: implementation contract for issue #59. Broad page-family adoption belongs
to #64; this ticket proves the contract on tutor, event, fleet and member workflows.

## Actions and task regions

- A form or task region has one visually primary completion action. Secondary
  actions and **Cancelar** links follow it; destructive actions are visually
  distinct and use specific object-and-effect wording.
- A link performs navigation and a button submits a mutation. Labels describe the
  result—**Adicionar menor**, **Criar evento**, **Agendar manutenção**—rather than
  generic “OK”, “Enviar” or “Guardar” where a more specific label exists.
- Cancel returns to the owning list or detail route. Post/redirect/get returns to
  the same owner after success. Authorization is always checked by the server.
- Destructive or difficult-to-reverse changes require proportionate confirmation
  that also works without JavaScript. Native required controls are preferred over
  custom dialogs.

## Fields and validation

- Required fields are identified in the label and forms explain the convention
  once. Optional fields say **(opcional)**. Help text precedes errors and is tied
  to its control with `aria-describedby`.
- Invalid submissions return `422`, preserve safe values and reopen the owning
  disclosure. Passwords and file inputs are intentionally never repopulated.
- A linked error summary appears before the invalid form, receives programmatic
  focus when JavaScript is available, and remains the first error landmark without
  JavaScript. Field-level messages remain next to their controls; invalid controls
  expose `aria-invalid` and reference their messages.

## Feedback and progressive enhancement

- Success, warning and failure feedback stays beside the task that caused it.
  Success uses `role=status`; blocking failures use `role=alert`. The same message
  is not repeated in a toast or a second live region.
- Normal HTML submission is complete. HTMX may replace only the owning region,
  disables its submit controls while pending, and restores focus to the returned
  summary or status message. It must not change validation, redirects or access.
- Pending state uses `aria-busy`; unavailable actions are explained in visible
  text instead of silently disappearing when that explanation is useful.

## Representative proof in #59

- simple invalid path: adding a minor;
- complex invalid path: event creation and maintenance scheduling;
- contextual mutation: repair status and maintenance completion;
- destructive action: member deactivation with a required, server-rendered
  confirmation and a return path to the member detail.

Remaining forms adopt these rules during #64 rather than expanding this ticket
into a full visual migration.
