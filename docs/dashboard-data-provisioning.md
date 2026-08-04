# Dashboard data provisioning

## Decision

Club operations owns the data presented by dashboards. Equipment is managed by
platform administrators through the audited fleet UI. Controlled imports remain
available for reviewed bulk provisioning; other dashboard read models continue
to use the controlled CLI/import process.

The authoritative write paths are:

| Dashboard data | Authoritative record | Operational owner |
|---|---|---|
| Equipment | `equipment` | Fleet administrator through `/admin/fleet`; reviewed import for bulk provisioning |
| Training logs | `training_logs` | Coach or sports director |
| Performance metrics | `performance_metrics` | Coach or sports director |
| News | `news_items` | Club communications administrator |
| WhatsApp groups | `whatsapp_groups` | Club communications administrator |

## Import contract

Each import must be prepared by its named operational owner and applied by a
platform administrator using a reviewed, version-controlled import file. An
import must be validated in the test database before it is applied to
production. The file must identify the operator, source system or document,
and effective date; those records are retained with the release/change record.

Imports must use the existing database constraints and types rather than
inventing dashboard-only fields:

| Record | Required operational checks |
|---|---|
| Equipment | Unique asset tag; valid type and status; retired items are excluded from repair choices. |
| Training log | Existing athlete ID; RFC 3339 occurrence time; duration and distance within database limits. |
| Performance metric | Existing athlete ID; valid metric type; label, unit, numeric value, and RFC 3339 measurement time. |
| News item | Portuguese title and summary; HTTPS URL when present; publication time and publication state. |
| WhatsApp group | Name, discipline, HTTPS group URL, active state, and zero or more curated programme, team, category, modality, event, or guardian audiences. |

An import is rejected as a whole if any row is invalid or references a missing
record. The operator must correct the source file and rerun it; partial data is
not an acceptable dashboard state.

## Scope boundary

The import path is operational provisioning, not an alternative interactive
editor. Equipment created or changed interactively is recorded in the immutable
equipment audit history. Issue #6 owns targeted announcements and any later
WhatsApp audience-management workflow.

Dashboard populated-state acceptance requires a successful import for the
relevant programme and a browser check against that imported data. Empty-state
coverage remains valid when no import has been applied.
