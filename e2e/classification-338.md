# #338 isolated browser regression

`classification-338.spec.mjs` is opt-in (`E2E_CLASSIFICATION_338=1`) and requires a **new disposable database**, never the default `make test-e2e`, `make db-provision-test`, or UI-review database. Provision an empty PostgreSQL 16 database from `internal/db/schema.sql`, then apply `e2e/classification-338-seed.sql` with `psql -v ON_ERROR_STOP=1`. The fixture adds six synthetic accounts, one current season, an out-of-range Competition category, one Leisure participation and active/revoked coach grants. Use a separate Compose project and named volumes, unique loopback ports, and an app pointed only at that database. Its synthetic password is the repository E2E password used in the spec. Start the app with the normal test configuration but a distinct `DATABASE_URL`, `BASE_URL` and `PORT`; use `E2E_BASE_URL` so Playwright does **not** start its default shared `.env` server. Run:

```sh
E2E_CLASSIFICATION_338=1 E2E_BASE_URL=http://127.0.0.1:<unique-port> npx playwright test e2e/classification-338.spec.mjs e2e/classification-age-exception.spec.mjs
```

Run serially against a fresh fixture: the correction test changes the subject's sporting selections. Do not reuse a database after this suite without reprovisioning it.

The classification page uses native `autofocus` on its `tabindex="-1"` task heading for normal responses and on the appropriate `role="alert"` error summary for 422 date/correction responses. The browser test asserts actual focus in all three states under the unchanged `script-src 'self'` CSP; no inline focus script is needed. Native CSS zoom is an approximation, not a substitute for manual browser zoom/screen-reader evidence. The installed `filippo.io/csrf/gorilla` compatibility layer enforces cross-origin Fetch Metadata/Origin checks, not form-token validation; the test verifies rejection of a cross-site request and lets real browser forms submit unchanged.
