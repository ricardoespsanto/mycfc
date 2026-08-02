# UI review harness

Issue #62 provides a disposable, deterministic dataset for authenticated design
review. It uses the dedicated PostgreSQL database `mycfc_ui_review`; resetting it
does not alter the ordinary `mycfc` development database.

## Personas

Every credentialed persona uses the test-only password `correct horse 7`.

| Persona | Login | Capabilities and useful state |
| --- | --- | --- |
| Ordinary member | `review-member@example.test` | Adult with no programme; sparse/empty state |
| Tutor | `review-tutor@example.test` | Two dependants with long Portuguese names |
| Athlete | `review-athlete@example.test` | Competition, K1, team, metrics, logs, sessions and events |
| Coach | `review-coach@example.test` | Competition-scoped coach; event/training authoring and dense plans |
| Administrator | `review-admin@example.test` | Members, news, 14 assets, 12 repairs and 8 maintenance tasks |
| Multi-capability | `review-multi@example.test` | Adult tutor, Competition, Leisure, coach and moderator with a dependant |

The SQL source is `scripts/ui-review-seed.sql`. IDs and copy are fixed; dates
relative to `now()` keep Today and upcoming-work states useful whenever the
harness runs.

## Commands

Install pinned tools once with `make tools`.

```bash
# Recreate only mycfc_ui_review and print the persona reminder.
make ui-review-reset

# Reset, then run the review dataset through Air on http://localhost:8080.
make ui-review-dev

# Reset and capture every agreed desktop/mobile screen in a pinned container.
make ui-review-screenshots

# Run one focused persona while ui-review-app is available.
docker compose --profile ui-review run --rm \
  -e UI_REVIEW=1 -e E2E_BASE_URL=http://ui-review-app:8080 \
  ui-review-capture npm exec playwright test e2e/ui-review.spec.mjs \
  --grep 'captures tutor'
```

`make ui-review-screenshots` captures 1440×900 desktop and 375×812 mobile
full-page PNGs under ignored `artifacts/ui-review/<persona>/<viewport>/`. The
same focused suite runs axe on every captured page and fails with the complete
serious/critical violation payload. Screenshots are review artifacts, not pixel
comparison baselines.

The existing application gate remains:

```bash
make verify-foundation
make test
make test-e2e
```

## Route and capability matrix

Legend: **V** visible/usable, **M** visible with management controls, **—** not
authorized. Every adult also has the tutor capability under the current rules.

| Route family | Navigation owner | Primary journey | Member | Tutor | Athlete | Coach | Admin | Multi |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `/today` | Hoje | Review today's relevant activity | V | V | V | V | V | V |
| `/events`, `/events/{id}` | Atividade → Eventos | Browse/respond; staff manage scoped events | V | V | V | M | M | M |
| `/treinos` | Atividade → Treinos | Athlete records work; staff author plans/sessions/docs | V | V | V | M | M | M |
| `/announcements`, `/announcements/{id}` | Atividade → Avisos | Read notices; authorized staff author | V | V | V | M | M | M |
| `/dashboard/guardian` | Os meus espaços → Menores a cargo | View and add dependants | V | V | V | V | V | V |
| `/dashboard/leisure` | Os meus espaços → Lazer | Leisure news, groups and calendars | — | — | — | — | — | V |
| `/dashboard/initiation` | Os meus espaços → Iniciação | Programme training/performance workspace | — | — | — | — | — | — |
| `/dashboard/competition` | Os meus espaços → Competição | Programme training/performance workspace | — | — | V | — | — | V |
| `/dashboard/kayak-polo` | Os meus espaços → Kayak polo | Programme training/performance workspace | — | — | — | — | — | — |
| `/dashboard/coach` | Compatibility route; target Eventos | Current sparse coach pointer | — | — | — | V | — | V |
| `/dashboard/moderator` | Deferred capability route | Current placeholder | — | — | — | — | — | V |
| `/admin/membros`, `/admin/membros/{id}` | Administração → Membros | Search/create/manage accounts | — | — | — | — | M | — |
| `/admin/noticias` | Administração → Notícias | Draft/publish/expire club news | — | — | — | — | M | — |
| `/admin/fleet` | Administração → Frota | Repairs, equipment and maintenance | — | — | — | — | M | — |

POST actions inherit the visibility and ownership of their GET page. `/repairs`
is a contextual action available to every authenticated persona. Compatibility
redirect decisions remain normative in `docs/design/information-architecture.md`.

## Screenshot set

The harness captures representative current-state screens rather than every route:

- member: Today, Eventos, Avisos;
- tutor: Today, Menores a cargo;
- athlete: Today, Competição, Treinos;
- coach: Today, Eventos, Treinos;
- administrator: Today, Membros, Frota;
- multi-capability: Today, Lazer, Competição, Eventos.

This set covers sparse and dense pages, ordinary content, tutor/dependant
management, staff authoring, administration, and cumulative navigation. The
final route-wide evidence and automated/manual/deferred classification is in
`ui-release-gate.md`.
