# Isolated group setup acceptance (#392/#393)

Run from the integrated application checkout:

```sh
python3 e2e/run-training-groups.py /tmp/mycfc-training-groups-evidence
```

The evidence path must be new and outside all source checkouts. `--app-source /path/to/integrated/source` optionally compiles another frozen checkout while reading these test files from the current checkout. Docker, Go and npm are prerequisites; this runner installs the exact lockfile dependencies into its evidence directory. No `.env`, shared local services, private screenshot or club identities are used. The spec is opt-in and is skipped by ordinary browser runs.

The runner creates its own uniquely named PostgreSQL **16.9** container, bound only to loopback, with `Europe/Lisbon` timezone and the full baseline plus migration ledger including `reset-baseline-v1`. It builds the real server, starts an isolated loopback application port, and runs pinned **Playwright 1.63.0** as the calling UID/GID with read-only test sources and dependencies. Only its new evidence directory is writable to the browser. Each selected phase runs with one worker and zero retries; unrelated phases are excluded rather than reported as successful skips.

The first fixture has configured programmes and a synthetic coach grant but **zero seasons, athletes and groups**. The actual create route must allow an empty group before a season exists. A second SQL fixture then provisions a synthetic season, members and exact team grants to exercise roster and week routes. This provisioning is test setup, not acceptance of #404's complete real-world first-setup journey.

Covered routes and behavior:

- Empty list, permitted programme selection independent of memberships, empty-group creation and exact landing.
- Native no-JavaScript create, name search, multiple checkbox selection, add and rename at 320px.
- Blank search yields no roster; literal `%` stays literal; names are scoped and no email, date of birth or unrelated names/profile links appear.
- Atomic wrong-programme and wrong-team batches; occupied-other-group rejection under the one-active-group rule; duplicate IDs rejected and already-added athletes remain idempotent.
- Concurrent add/rename forms produce conflict feedback, retain submitted values, and require review.
- Real draft week creation, exact group/week context and preserved week after rename; additions do not create publications or prescriptions.
- Exact programme versus team grants, revoked/unscoped actor denial, cross-origin CSRF rejection with the authenticated cookie present, ADMIN_ONLY and DISABLED feature modes.
- Actual route axe analysis at 320px and desktop, horizontal overflow checks, keyboard focus/order, server validation semantics and focused feedback. Serious/critical axe findings fail the test.

The runner retains JSON reports, axe results, failure traces, synthetic application logs, source/test manifests and binary hashes outside the checkout. A separate loopback attacker origin serves one native negative-test form without serving evidence files. It stops both local servers and removes only its owned containers and anonymous volumes on success or failure, then records port/container cleanup. It does not clear evidence. Failed executions remain failed evidence; use a new evidence path after correcting a fixture or test defect.

Manual screen-reader behavior, real browser zoom, coach observation and the remaining #391/#404 workflow are not established by this suite. Season setup, athlete moves/removals, the focused week workspace and workout authoring remain separate stories under #391.

The existing `filippo.io/csrf` v0.2.1 dependency verifies Fetch Metadata/Origin rather than requiring legacy form tokens. The negative test therefore submits a genuine native form from a second loopback port, verifies that the authenticated session cookie is sent and the origin differs, and requires 403 with the original group name unchanged. A same-origin tokenless form is not an attack under that dependency contract.

During harness development, inactive membership attachment ordering, overly broad selectors, axe injection into a JavaScript-disabled context, and a token-based CSRF assumption were corrected. Those failed runs are not passing acceptance evidence. Actual routed validation also exposed empty-valued `aria-invalid` attributes, while visual review found unreadable squeezed mobile header actions. Those two application defects were fixed with explicit ARIA boolean strings and a scoped mobile wrapping rule, then covered by semantic render and routed layout assertions.

Observed isolated run on 9 November 2026: `/tmp/mycfc-391-browser-verified/summary.json` records **12 selected cases passed**, no retries or skipped selected cases. Seventeen actual-route axe analyses passed; sixteen distinct state reports are retained (the 320px create task was analyzed in two phases under the same output name), all with zero serious/critical findings. SQL postconditions were two members in the created group, one preserved draft week, zero publications/prescriptions and zero partial invalid-batch additions. Both local servers stopped, all three owned ports closed, and no owned containers remained (`cleanup.json`).

The tested application was clean source commit `6b6a84b470f8343b1b203312afcbefe31f0d2d56`; source manifest SHA-256 `6d01f70f4bff27200225290f94b5f19fb304f6d80903b5c6794636b2472c72bb`, compiled binary SHA-256 `95b09c055cd4ee18d04a97b6451234d649f6387d025659a71b17259308188ec3`. PostgreSQL 16.9 used Europe/Lisbon and a complete 78-entry ledger; the browser was Playwright 1.63.0 with axe 4.13.0. New-spec ESLint, JavaScript/Python syntax, scoped UI render regression tests, CSS lint, asset build and diff whitespace checks passed.

The original test manifest is retained. During the successful run, the spec received only the explicit `import { URLSearchParams } from 'node:url';` required by ESLint; it names the same Node standard constructor already available globally and changes no assertions or application behavior. `test-manifest-final.json` and `test-manifest-drift.json` preserve the final hash and prove that removing exactly this import reproduces the initial spec hash. This drift is separate from earlier failed harness iterations.

Actual screenshots include `detail-empty-320.png`, `search-results-320.png`, `validation-320.png` and `roster-1280.png` in that evidence directory. Screenshots support visual review; assertions, axe output and SQL postconditions provide the automated verification.
