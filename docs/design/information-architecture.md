# Authenticated information architecture

Status: approved in issue #54. The authenticated shell was implemented in #55;
the approved route and ownership migration was implemented in #56.

## Evidence and boundary

This proposal is based on `internal/app/router.go`, the shared navigation and
page metadata, handler authorization, the implemented page templates, current
browser tests, and live walkthroughs of administrator, ordinary adult, tutor,
and competition-athlete accounts on 1 August 2026. Coach and moderator behaviour
was checked against the route guards and rendered pages because deterministic
staff personas do not yet exist; issue #62 owns those fixtures.

The live walkthrough confirmed that:

- Today is the post-login destination but is represented only by the MyCFC logo.
- Events, Treinos and Avisos were originally always-visible task destinations;
  on 5 August 2026 reader-facing Avisos moved to the global bell.
- every non-dependent adult receives the Tutor workspace, including athletes;
  programme and staff capabilities are additive rather than exclusive roles.
- the administrator sees Membros, Notícias and Frota in both the main navigation
  and a second administration sub-navigation.
- event detail correctly keeps Eventos selected; equivalent parent ownership is
  required for every nested and detail route.
- `/dashboard/member`, `/dashboard/competitor`, `/dashboard/coach` and
  `/dashboard/moderator` are absent from ordinary task navigation. The first two
  duplicate or weaken Today/programme destinations; the latter two are sparse or
  placeholder capability pages.

This ticket changes no routes, authorization, templates, or styling.

## Current route, capability and task inventory

`Authenticated` means any active account. Programme and staff checks are
cumulative. `Tutor` below is the existing `RequireGuardian` rule: any active
adult account, not an exclusive persisted role.

| Method and route | Current capability | Primary task and current owner | Assessment |
| --- | --- | --- | --- |
| `POST /logout` | Authenticated | End the current session; header account action | Keep; account action, never task navigation |
| `GET /dashboard` | Authenticated | Redirect to `/today` | Keep as compatibility alias |
| `GET /today` | Authenticated | Events relevant today; logo only | Canonical home; add explicit **Hoje** navigation |
| `GET /dashboard/member` | Authenticated | Explain that no programme is active; no navigation entry | Orphaned duplicate of the empty Today state |
| `GET /dashboard/competitor` | Competition, Initiation or Kayak Polo | Combined athlete dashboard; no navigation entry | Ambiguous legacy aggregate beside specific programme pages |
| `GET /dashboard/initiation` | Initiation | Training, performance, calendars and groups; O meu programa | Keep canonical workspace |
| `GET /dashboard/competition` | Competition | Training, performance, calendars and groups; O meu programa | Keep canonical workspace |
| `GET /dashboard/kayak-polo` | Kayak Polo | Training, performance, calendars and groups; O meu programa | Keep canonical workspace |
| `GET /dashboard/leisure` | Leisure | News, calendars and groups; O meu programa | Keep canonical workspace |
| `GET /dashboard/guardian` | Tutor | View/add minors and report a repair; O meu programa | Keep route; relabel destination **Menores a cargo** |
| `POST /guardian/add-dependent` | Tutor | Add a minor from `/dashboard/guardian` | Keep as child action of Menores a cargo |
| `GET /dashboard/coach` | Coach | Sparse explanation pointing to Eventos; Gestão | Remove as a navigable destination; preserve as redirect |
| `GET /dashboard/moderator` | Moderator | Placeholder with no implemented moderation tools; Gestão | Remove from task navigation; defer real destination |
| `GET /events` | Authenticated | Browse events, respond, and conditionally author; Eventos | Keep canonical top-level task |
| `GET /events/{id}` | Authenticated and event-visible | Read/respond; administer capacity and check-in when allowed | Keep as Eventos detail; parent stays selected |
| `POST /events/{id}/responses` | Authenticated and event-visible | RSVP from event detail | Keep as event child action |
| `POST /admin/events` | Administrator or coach | Create an event in `/events` within authorized scope | Keep route and contextual authoring |
| `POST /admin/events/{id}/confirm` | Administrator or coach | Confirm a waitlisted participant from event detail | Keep as event child action |
| `POST /admin/events/{id}/check-in` | Administrator or coach | Record attendance from event detail | Keep as event child action |
| `GET /announcements` | Authenticated | Browse visible notices | Keep as the full-page and bookmark view; omit from task navigation |
| `GET /announcements/panel` | Authenticated | Load the unread count and six most recent visible notices | Global bell fragment; private and non-cacheable |
| `GET /announcements/{id}` | Authenticated and announcement-visible | Read a notice or official document | Open from the bell and mark read on detail |
| `POST /admin/announcements` | Administrator or coach | Create a scoped notice in `/admin/avisos` | Keep route and dedicated authoring workspace |
| `POST /admin/announcements/{id}/publish` | Administrator or coach | Publish an authored notice | Keep as Avisos child action |
| `POST /admin/announcements/{id}/expire` | Administrator or coach | Expire an authored notice | Keep as Avisos child action |
| `GET /treinos` | Authenticated | View assigned sessions/documents; conditionally manage training | Keep canonical top-level task |
| `POST /treinos/planos` | Administrator or coach | Create a scoped training plan | Keep as Treinos child action |
| `POST /treinos/sessoes` | Administrator or coach | Create a session from a plan | Keep as Treinos child action |
| `POST /treinos/sessoes/resultados` | Authenticated assigned athlete | Record own training outcome | Keep as Treinos child action |
| `POST /treinos/documentos` | Administrator or coach | Publish a scoped official competition document | Keep as Treinos child action |
| `POST /repairs` | Authenticated | Report an equipment fault from programme/tutor dashboards | Keep contract; expose as a contextual action, not a destination |
| `GET /admin/membros` | Administrator | Search and create accounts; Administração and duplicate local nav | Keep canonical administration task |
| `POST /admin/membros` | Administrator | Create an adult or dependent account | Keep as Membros child action |
| `GET /admin/membros/{id}` | Administrator | Inspect a member and manage their account | Keep as Membros detail with active-subject context |
| `POST /admin/membros/{id}/inscricao` | Administrator | Change the member's active programmes | Keep as member child action |
| `POST /admin/membros/{id}/desativar` | Administrator | Deactivate the member | Keep as member child action |
| `POST /admin/membros/{id}/credencial-menor` | Administrator | Issue or recover a minor credential | Keep as member child action |
| `GET /admin/noticias` | Administrator | Draft, schedule, publish and expire club news | Keep canonical administration task |
| `POST /admin/noticias` | Administrator | Create a news item | Keep as Notícias child action |
| `POST /admin/noticias/{id}/publicar` | Administrator | Publish a news item | Keep as Notícias child action |
| `POST /admin/noticias/{id}/expirar` | Administrator | Expire a news item | Keep as Notícias child action |
| `GET /admin/fleet` | Administrator | Review fleet, repairs and maintenance | Keep canonical administration task |
| `POST /admin/maintenance` | Administrator | Schedule maintenance from Frota | Keep as Frota child action |
| `POST /admin/repairs/status` | Administrator | Change repair status from Frota | Keep as Frota child action |
| `POST /admin/maintenance/{id}/complete` | Administrator | Complete maintenance from Frota | Keep as Frota child action |

Health, static assets, landing, login and registration routes are outside the
authenticated IA. Unknown routes and method mismatches remain system concerns.

## Target sitemap and navigation

The shell presents task/content destinations first. Visibility is additive:
the same account may receive items from several groups without entering a role
mode.

```text
MyCFC
├── Avisos                       global bell; `/announcements` as full-page view
├── Hoje                         /today
├── Atividade
│   ├── Eventos                  /events
│   └── Treinos                  /treinos
├── Os meus espaços             only when at least one item is available
│   ├── Menores a cargo          /dashboard/guardian   (all adults)
│   ├── Lazer                    /dashboard/leisure
│   ├── Iniciação                /dashboard/initiation
│   ├── Competição               /dashboard/competition
│   └── Kayak polo               /dashboard/kayak-polo
├── Administração               authorized staff only
│   ├── Avisos                   /admin/avisos
│   ├── Membros                  /admin/membros
│   ├── Notícias                 /admin/noticias
│   └── Frota                    /admin/fleet
└── Conta
    ├── signed-in identity       display only; no profile route exists
    ├── capability summary       display only
    └── Terminar sessão          POST /logout
```

### Navigation rules

1. **Hoje is an explicit first-class item.** The brand may still link to it, but
   the logo is not its only affordance.
2. **Atividade is available to every authenticated account.** Avisos are consumed
   through the global bell; authorized staff author them in Administração.
3. **Os meus espaços is additive.** Use destination nouns—Lazer, Iniciação,
   Competição, Kayak polo and Menores a cargo—not labels that pretend the user
   has switched to one active role.
4. **Staff capability is context, not a destination.** Treinador and Moderador
   appear in the capability summary. Implemented coach actions remain inside
   their owned task pages. Do not keep empty role dashboards in primary navigation.
5. **Administration has one representation.** Keep its Membros, Notícias and
   Frota destinations in the main shell and remove the duplicate `AdminSubNav`.
6. **Account actions are separate from task navigation.** The shell shows the
   signed-in name and logout together. A profile link is not shown until a real
   profile route exists.
7. **Current state follows route ownership, not exact URL equality.** Details and
   mutations returning to a detail page retain their parent destination. Query
   strings never affect selection.
8. **Mobile changes presentation, not hierarchy.** Every destination and logout
   remains reachable from the interactive shell; disclosure state must not conceal
   the current destination.

## Cumulative capabilities and active subject

### Signed-in identity

Show the account name once in the persistent shell. “Administrador”, “Tutor”,
programme participation and staff grants are capabilities of that identity;
they are not alternate identities and must not replace the person's name.

### Capability summary

Use user-facing European Portuguese labels and list all applicable capabilities:

- **Tutor** for a non-dependent adult allowed to manage minors;
- **Lazer**, **Iniciação**, **Competição**, **Kayak polo** for active programme
  memberships;
- **Treinador** plus a human-readable programme/team scope where available;
- **Moderador** for an active moderation grant;
- **Administrador** for the platform administration grant.

Do not expose database values such as `Competition`, `Kayak_Polo`, `COACH` or
`ADMIN`. Do not provide a role switcher. Capability-dependent controls are added
to their task pages and server-side authorization remains authoritative.

### Active subject context

The active subject answers “whose record am I acting on?” and is independent of
the signed-in identity and capabilities.

- On `/admin/membros/{id}`, show **A gerir: {member name}** near the page title.
- A future dependent detail flow must show **Menor: {dependent name}**; the
  current tutor page is a list/form and has no active dependent selection.
- Event participants, equipment and authored content are page objects, not active
  identities; identify them in page headings rather than the account area.
- Clear subject context when returning to a collection or changing to an unrelated
  area. Never persist it as a global role/mode switch.

## Local navigation and breadcrumbs

- Top-level task and workspace pages do not need a breadcrumb merely repeating
  their heading.
- Detail pages use a short breadcrumb whose first link supplies useful return context:
  `Eventos > {evento}`, `Hoje > {aviso}`, and
  `Administração > Membros > {membro}`.
- “Administração” and “Os meus espaços” may be plain breadcrumb text until a real
  landing route exists; do not create non-functional links.
- Local navigation is reserved for stable sibling views within one destination.
  It must not duplicate the global destinations. The current administration
  sub-navigation therefore retires when the new shell is implemented.
- Parent selection rules are explicit:
  `/events/{id}` → Eventos, `/announcements/{id}` → no task-navigation selection,
  `/admin/membros/{id}` → Membros, and all action responses inherit the GET page
  they render or redirect to.
- Page titles follow `{page} | MyCFC`; avoid generic “Painel de” and “Área de”
  where the destination name is sufficient.

## Existing-to-target route decisions

Issue #56 applies these approved changes with `303 See Other` for authenticated
GET compatibility aliases, preserves query parameters, and retains server-side
authorization on every target.

| Existing route | Target decision | Reason |
| --- | --- | --- |
| `/dashboard` | Keep redirect to `/today` | Stable generic post-login alias |
| `/today` | Keep canonical | Authenticated home and cross-capability summary |
| `/dashboard/member` | Redirect to `/today` | Orphaned empty-state page duplicates home |
| `/dashboard/competitor` | Redirect to `/today` | Aggregate route is ambiguous for multi-programme accounts; specific workspaces remain |
| `/dashboard/initiation` | Keep canonical | Distinct existing programme workspace |
| `/dashboard/competition` | Keep canonical | Distinct existing programme workspace |
| `/dashboard/kayak-polo` | Keep canonical | Distinct existing programme workspace |
| `/dashboard/leisure` | Keep canonical | Distinct existing programme workspace |
| `/dashboard/guardian` | Keep canonical; label Menores a cargo | Stable contract and clear task destination |
| `/dashboard/coach` | Redirect to `/events` | Current page is only a pointer to the implemented event-management task; Treinos remains directly available |
| `/dashboard/moderator` | Keep temporarily but remove from primary navigation | No moderation task exists yet; redirect target is deferred rather than invented |
| `/events` and `/events/{id}` | Keep canonical | Shared attendee and staff task family |
| `/announcements` | Keep full-page view, remove from navigation | Bell is the primary reader surface; the route preserves bookmarks and a focused reading view |
| `/announcements/{id}` | Keep canonical | Stable notice and official-document detail route |
| `/admin/avisos` | Keep canonical | Dedicated administrator/coach authoring workspace |
| `/treinos` | Keep canonical | Shared athlete and staff task family |
| `/admin/membros` and `/admin/membros/{id}` | Keep canonical | Administration member family |
| `/admin/noticias` | Keep canonical | Administration news family |
| `/admin/fleet` | Keep canonical | Administration fleet family |
| All current POST routes | Keep paths and contracts | No benefit justifies breaking forms or integrations |

## Naming rules

- Use European Portuguese, sentence case and short destination nouns.
- Use **tutor/tutores** for people and **Menores a cargo** for the task destination.
- Use **Hoje**, **Eventos**, **Treinos**, **Avisos**, **Lazer**, **Iniciação**,
  **Competição**, **Kayak polo**, **Administração**, **Membros**, **Notícias** and
  **Frota** consistently in navigation, headings, metadata and breadcrumbs.
- Reserve **Treinador**, **Moderador** and **Administrador** for capability context;
  do not use them as empty destination labels.
- A programme membership is not an authentication role. Avoid “mudar de papel”,
  “perfil ativo” or wording that implies only one programme/responsibility applies.
- Internal English identifiers and stable paths follow `docs/terminology.md` and
  remain unchanged unless the redirect table explicitly decides otherwise.

## Decisions, assumptions and deferred questions

### Decisions

- Today is the explicit authenticated home.
- Task/content navigation owns Eventos and Treinos. The global bell owns notice
  consumption, while Administração owns authorized Avisos management.
- Programme and tutor destinations sit under Os meus espaços and accumulate.
- Staff and administrator grants are presented as capabilities, never an
  exclusive active role.
- Administration appears once; duplicated page-local administration navigation
  is removed.
- Parent route ownership drives selection and breadcrumbs.
- Stable mutation routes and authorization contracts remain unchanged.

### Assumptions

- The current authorization rule intentionally allows every adult to add/manage
  minors, so Menores a cargo is available to every adult even when the list is empty.
- No account/profile settings route exists in the current product.
- Specific programme dashboards remain useful until the Today vertical slice and
  later page-family work provide evidence for consolidation.
- #55 can expose human-readable capability scope using existing memberships and
  staff grants; it must not invent an active-role session value.

### Deferred, with owner

- **Moderator tools and destination:** no functional workflow exists; define it in
  a focused feature issue before redirecting/removing `/dashboard/moderator`.
- **Dependent detail navigation:** no route exists; decide only with an approved
  member/dependent workflow ticket.
- **Profile/account settings:** add a navigation item only when a real route and
  task are implemented.
- **Programme workspace consolidation:** reassess after #57 demonstrates the Today
  content model; do not fold it into #55 or #56.
- **Administration landing:** not required for this hierarchy. Add one only if
  later evidence shows the disclosure/direct links are insufficient.
- **Deterministic coach, moderator and multi-capability screenshots:** #62 supplies
  the personas and route matrix before #63 visual exploration.

## Review gate

Approval of this document authorizes #55 and #56 to implement only the decisions
above. Review should specifically confirm:

1. the four persistent areas: Hoje, Atividade, Os meus espaços and Administração;
2. Menores a cargo as the tutor destination label;
3. removal of role-dashboard links for coach/moderator and the stated redirects;
4. removal of duplicate administration sub-navigation; and
5. the no-role-switcher capability and active-subject model.

Do not begin route migration or authenticated-shell implementation until those
points are approved.
