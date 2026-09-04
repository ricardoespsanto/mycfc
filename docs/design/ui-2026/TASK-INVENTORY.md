# UI 2026 task-surface inventory

Status: delivered inventory for [#188](https://github.com/ricardoespsanto/mycfc/issues/188) and River Clubhouse 2.0. It records the implemented task-surface contract without changing routing, authorization or business behaviour on its own.

## How to use this inventory

The interaction selection is the approved #187 model: inline for an immediate low-risk change; centred modal for a short bounded contextual task; side sheet for a medium task; dedicated task route for long, conditional, sensitive, file-based or comparison-dependent work; and a confirmation dialog backed by a guarded task route for destructive work. A disclosure is supporting detail, not a substitute for a task surface.

All mutations remain server-authoritative and CSRF-protected. A task must retain its owning object and return context; invalid safe values return to that same task with a linked error summary. Password and file values are intentionally not restored.

## Route-family map

| Family and actors | Current task types | Approved target surface | Canonical URL and return context | Migration issue |
| --- | --- | --- | --- | --- |
| Shell, navigation and multi-capability adults | Navigate, open account and notice context | Inline controls; non-modal mobile menu drawer | Existing destination URL; return focus to exact opener | #191 |
| Collections and staff queues: members, fleet, suggestions, albums, news | Search/filter, select record, quick state change | Inline filter/state control; named horizontal region for true tables; modal action menu only when bounded | Collection GET retains query, tab, page and scroll anchor | #192 |
| Administrative bounded creation: member, equipment, maintenance, repair, album, news, suggestion triage | Short creates; local status updates | Centred modal for short forms; inline for one-step state; side sheet only where fields need contextual reference | Collection URL or object detail; preserve filter/page | #193 |
| Events and announcements: members, tutors, coaches, administrators | RSVP, create/edit event, author announcement, publish/expire/cancel | Inline RSVP; dedicated route for long conditional event edit; bounded announcement form modal; guarded confirmation for publish/expire/cancel | Event or announcement GET is canonical; return to originating object | #194 |
| Ordinary training: athlete, guardian, coach, administrator | Plan/session authoring, outcome, feedback, correction/replacement, cancellation | Dedicated route for session edit; modal for bounded creation; inline outcome selection; dedicated/side-sheet task for replacement and feedback; confirmation for cancellation | Session/plan task URL retains subject and source collection | #194 |
| Structured training: coach and administrator; read-only athlete/guardian | Build weeks/sessions, nested prescriptions, copy, variations, routines, publication | Builder workspace with stable selection; dedicated routes for water/gym/variation tasks; bounded add/copy/save in modal or side sheet; confirmation for retire/publish | Stable group/week/day/session/item selection in GET URL; exact task returns after 422 | #195 |
| People, profile, family, system and authentication | Edit self/dependant/member profile, photo, membership, credential, deactivation, feature settings, login/recovery | Dedicated sectioned route for sensitive/long profile and file tasks; inline resend/settings; confirmation for photo removal/deactivation | Profile/member task URL names active subject; safe section values preserved | #196 |
| Cross-cutting verification | Pending, invalid, conflict, success, disabled, destructive, asset failure | Shared contract evidence, not a new end-user surface | Persona/route/state is named in every test | #197 |

## Interaction decisions for implemented mutation families

| Owning object | Task | Surface | State and recovery contract |
| --- | --- | --- | --- |
| Account | Login, registration, password recovery/reset | Dedicated route | Invalid response remains at its URL; focus error summary. Passwords never repopulate. |
| Profile / dependant profile | Edit identity/contact/emergency/medical/address fields | Dedicated sectioned route | Persistent signed-in and active-subject context; return to failing section. |
| Profile photo | Upload / replace | Dedicated route task area | File selection is not restored; clear consent/effect is visible. |
| Profile photo and member account | Remove photo / deactivate account | Confirmation dialog backed by guarded route | Names the subject and permanent effect; cancel returns focus to opener. |
| Guardian workspace | Add dependant; dependant leaderboard privacy | Modal for add; inline checkbox/save for privacy | Add failure remains in modal; privacy returns a local status. |
| Fleet repair | Report repair with photo | Dedicated route or task side sheet | File and validation needs rule out a trailing disclosure; preserve textual values only. |
| Fleet / maintenance | Add/edit/retire/reactivate equipment; schedule/complete maintenance; repair status | Modal for bounded add/status; dedicated edit route; confirmation for retirement; inline one-step status changes | Return to selected fleet tab, filtered item and local status region. |
| Member directory | Search; create account; alter membership; issue minor credential | Inline search; modal create; dedicated contextual credential/membership task | Subject remains explicit; credential secret is never preserved or echoed. |
| Club news | Draft; schedule; publish; expire | Modal/side-sheet draft depending on conditionality; confirmation for publish/expire | Return to item and list filter; describe effective publication state. |
| Event | RSVP; event authoring/edit; confirm place/check-in; cancel | Inline RSVP and bounded staff controls; dedicated author/edit; confirmation for cancellation | Event audience, capacity and acting subject stay visible; errors return to task. |
| Announcement | Create draft; publish; expire | Bounded authoring modal; confirmation for lifecycle action | Audience summary is visible before submit and lifecycle effect is named. |
| Ordinary training | Create plan/session; edit/cancel session; athlete outcome, feedback/distance/replacement | Modal creation; dedicated edit; inline simple outcome; bounded contextual task for feedback/replacement; confirmation cancel | Session, athlete/dependant and current outcome remain explicit. |
| Structured training | Group/week/session/segment/block creation; copy; routines; variation; publication | Builder selection plus bounded modal/side sheet; dedicated nested editor; confirmation for publication/retirement | 422 returns to exact selected object. Nested modals are prohibited. |
| Suggestions | Submit; staff filter/triage | Bounded modal submit; inline filters; side sheet or dedicated triage based on response length | Private author history remains private; conflict returns to the same staff item. |
| Albums | Create/archive scoped album | Bounded create modal; confirmation archive | Scope and audience appear before commit. |
| Feature settings | Update feature availability | Inline only when one bounded setting; otherwise side sheet | Unknown/invalid values fail closed and explain status. |

## Responsive and accessible behaviour

- At 375px and 320px, modal and side-sheet tasks become full-height surfaces with sticky title, close action and primary action footer.
- A modal traps focus, blocks background interaction, supports Escape only where discard is safe, and returns focus to its exact opener. Dirty dismissals require a discard confirmation.
- Dedicated task routes document Back and refresh as ordinary recovery actions. Return links preserve a safe `return_to` only after server validation.
- True comparison tables use a labelled horizontal scroll region. Collections can instead become cards or lists when the comparison semantics are not lost.
- Loading, pending, invalid, conflict, success, disabled and destructive states must be represented in the page-family evidence. Status is textual and structural, never colour-only.

## Representative approval walkthroughs

1. Tutor adds a dependant, sees a validation error, corrects it and returns to the same dependent list.
2. Athlete records or replaces an ordinary training outcome without losing the selected session or acting-subject clarity.
3. Coach creates then corrects a scoped event and sees audience/capacity context before publication.
4. Administrator changes a member account, confirms deactivation and returns to that member record.
5. A multi-capability adult changes area and opens a bounded task on mobile without a role switch or lost navigation context.

## Evidence inputs

- Existing interaction contract: [`docs/design/interaction-contract.md`](../interaction-contract.md).
- Current complete route registration: [`internal/app/router.go`](../../../internal/app/router.go).
- UI 2026 editable design packet: [`README.md`](README.md), [`interaction-surfaces.svg`](interaction-surfaces.svg), [`collection-task.svg`](collection-task.svg), [`structured-training-planner.svg`](structured-training-planner.svg) and [`mobile-task.svg`](mobile-task.svg). These are copied from the #188 planning branch so the approval packet is self-contained.
