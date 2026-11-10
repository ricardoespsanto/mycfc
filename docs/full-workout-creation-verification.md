# Focused workout creation

This local #391 increment expands the focused task to structured water, gym,
ordered mixed activities, competition and notes-only rest/logistics. It is CREATE
only: identity-preserving editing, partial-plan publication and the rest of
#399/#400/#391 remain outstanding.

## Implemented behaviour

The same group/week/day route and native preview/confirmation forms handle the
whole workout. Coaches can keep instructions in their own words or progressively
add water repeat groups, effort/recovery, explicit targets, gym circuits,
supersets, loads and execution notes. Running can remain plain instructions.
Ordered activities share one session identity; separate appointments remain
separate sessions. No routine, profile or detailed prescription is compulsory.

Actual starts are required for training and competition. A complete explicit end
pair yields an interval; an unknown end stays SQL NULL. Rest and logistics also
support a date or named part of the day without invented clocks or effort.
Lisbon gaps/folds retain their existing explicit validation. Selected CREATE day
is fixed. Blank scaffolding is omitted; supplied contradictory content stays
visible until deliberately corrected or removed.

Native commands preserve incomplete values and removed subtrees. Generic editor
and edit-back retain trusted signed proof. Preview creates no draft/publication;
confirmation inserts and rereads the complete scoped graph atomically. Exact
replay cannot create another identity. Stale confirmation needs deliberate
re-preview with the original allocated identities. Unusable proof retains
copyable input and the week recovery link; unknown commit never triggers resend.

The existing instruction assembler and read-only renderer serve preview and
saved views. One checked totals calculation covers all repeat, recovery,
intensity and sum operations. Unknown/estimated values remain distinct, malformed
graphs show unknown totals, and a continuation target is not added as extra
distance or used to invent an end time.

## Data and protection boundaries

Fresh schema 82 tightens the nullable-repeat CHECK and corrects current water
tree/sibling ordering, including newly emitted routine snapshots. Stored routine,
publication, prescription and outcome bytes are not converted or rewritten.
Existing-record migration/backfill/legacy upgrade compatibility is not a gate.

Every new graph query repeats actual actor and exact programme/team authority.
The writer admits the expected state first on the existing pinned transaction,
then checks current scope, versions, references, complete raw readback and final
expiry. The retired plain writer is removed; its regressions exercise the graph
writer. No new role, dependency, control grant or transaction framework is added.

Profile selection is explicit. The metadata catalogue has a 100+1 overflow
sentinel and offers no partial/default selector on overflow. Selected profile and
zone facts are complete, bounded and signed. Supported zone changes version the
profile row; ordered SHARE NOWAIT locks and reread detect concurrent updates and
insertions. A conflict is retained for review, without automatic retry.

Whole-task ceilings include removals: eight activities, 32 phases, 256 water
nodes at depth eight and 256 exercises. References are limited to 32 selected
profiles and 256 zones. Both encoded confirmation/edit-back requests and the
actual PostgreSQL BASE artifact have separate size limits. Oversized graphs and
count-plus-one results are refused as a whole. These are safety limits, not a
maximum-success capacity or response-time promise.

## Verification

- Full Go tests, vet, tagged compilation and pinned generation passed. The root
  assembly independently passed full Go tests and vet and matched every runtime
  hash used by the browser run.
- Fresh proper-role PostgreSQL checks cover raw hybrid round-trips, all timing
  kinds, scoped access, final-detail rollback, replay, stored history/outcomes,
  reference update/insert races, whole-task bounds and current tree ordering.
  An actual two-week case preserves the original descending outer week order.
  EXPLAIN confirms the recorded oversized raw-tree fixture is capped before
  recursive traversal; it is not an end-to-end performance claim.
- Real app/router/SCS/CSRF checks passed for the original plain flow and native
  structured water, gym and run in one workout. Minimum PostgreSQL regressions
  now exercise the production graph writer, including real existing history and
  the defensive 200000/200001-byte artifact boundary.
- Independent data and composed QA/security reviews found no material blocker.
  Generic proof, unused-phase, fixed-date and optional-end form corrections were
  independently identified and checked before routed execution.
- Actual pinned Chromium passed **6/6 in 57.3 seconds**, with no skips, retries or
  flaky results. Cases include the three plain regressions, structured water with
  eight-level nesting and stable identities after stale review, native no-JS
  320px circuit/superset plus run, and rest/logistics without fabricated effort.
  Four axe scans had no serious/critical WCAG-tag violations. Nine screenshots
  were captured; the narrow gym preview and depth-eight editor were inspected.

Exact SQL assertions showed nine sessions, eight activities, thirteen blocks,
eight water nodes and three gym exercises in the synthetic fixture. There were
no publication, prescription or outcome writes. The browser's empty-history
fixture is not used as history-preservation proof; that has separate PostgreSQL
coverage. Preview/saved canonical notes, all typed values, ordering, nulls,
references and retained identities were checked.

The actual Go bootstrap, schema installation, explicit owned-local preparation
and hardening preceded synthetic seeds. Runtime/test hashes stayed unchanged.
The app stopped; root independently confirmed a reachable Docker daemon and
genuine absence of the exact two owned containers and volume. No existing or
production database, records, backups or actual `.env` were changed.

Private evidence is retained at
`/tmp/mycfc-full-workout-browser-4b819d2eaf70-attempt1`. This is not manual coach,
screen-reader, zoom, forced-colour, real-plan or natural-clock acceptance, full
`make verify`, release or deployment. Synthetic rhythm values are not approved
club guidance. Actual season dates remain unknown/configurable; the separate
temporary-placement workflow must return athletes to their previous group.
