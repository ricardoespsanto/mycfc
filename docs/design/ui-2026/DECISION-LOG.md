# UI 2026 decision log

Status: approved and delivered through River Clubhouse 2.0 (#187). The decisions below were implemented without changing club policy, authorization rules or business ownership.

| ID | Decision | Reason and consequence | Owner / gate |
| --- | --- | --- | --- |
| D-01 | Use task complexity and consequence to select inline, modal, side sheet, route, confirmation or disclosure. | Replaces the inconsistent mix of trailing creation disclosures, native dialogs and dedicated pages. | Approved in #188; delivered in #189–#197. |
| D-02 | JavaScript is expected, while server handlers remain the authority. | Removes duplicate no-JS presentation pressure without moving validation, authorization, CSRF or audit logic client-side. | Approved epic constraint. |
| D-03 | Long, conditional, sensitive, file-based and comparison-dependent work uses a dedicated route. | Refresh, Back and validation recovery are more reliable than a large dialog. This includes profile editing, file-heavy repair/photo work and complex event editing. | Delivered in #194/#196. |
| D-04 | A destructive action requires a confirmation dialog backed by a guarded route. | The dialog names the object, effect and permanence; it never replaces server authorization. | Delivered by each owning slice. |
| D-05 | Mobile modal and side-sheet tasks use full-height surfaces. | Prevents compressed multi-column tasks and obscured actions at 375px/320px and compact heights. | Delivered in #190. |
| D-06 | A mutation preserves owner, active subject, selected tab/filter/page and originating object. | Makes successful and invalid paths recoverable for multi-capability adults and staff. | Delivered and covered in #197. |
| D-07 | Structured training gets stable workspace selection and dedicated nested editors. | It is the highest complexity task family; eagerly rendered dialogs and generic rejected requests are not an acceptable recovery path. | Delivered in #195 after reconciling the active prescription work. |
| D-08 | No new business rules, schema migration or feature flag are introduced by the UI epic. | Keeps the work a workflow modernization and limits risk. | Epic boundary. |

## Outstanding human decisions

None are known from the delivered code and issue set. Any future club-policy question must be recorded here before it is inferred in templates or handlers.

## Approval record

The product authority approved the route-family map, interaction decisions, responsive contracts and representative walkthroughs, then explicitly authorized implementation, GitHub publication and release for the delivered epic.
