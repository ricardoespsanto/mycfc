# #335 definition-authoring browser regression

`definitions-335.spec.mjs` is opt-in (`E2E_CLASSIFICATION_338=1`). Use a **new disposable database**, not the default integration, UI-review, or local Compose database. Provision it from `internal/db/schema.sql`, then apply `e2e/classification-338-seed.sql` and `e2e/definitions-335-seed.sql` in order. The latter adds a synthetic Competition coach without changing the #338 fixture or #339 event files. Start the app with `DATABASE_URL` pointed only to this database, a distinct `BASE_URL` and port, and `APP_ENV=test`; set `E2E_BASE_URL` to that app so Playwright does not start its shared `.env` server.

Run Playwright in the repository-pinned `mcr.microsoft.com/playwright:v1.63.0-noble` image (with the repository mounted as `/workspace`, the app reachable from the container, and `npm ci` installed in the workspace):

```sh
E2E_CLASSIFICATION_338=1 E2E_BASE_URL=http://<isolated-app>:<port> npx playwright test e2e/definitions-335.spec.mjs
```

Run serially on a fresh fixture: the authoring test creates a definition. Coverage checks the classification-task link, direct scope access for Competition coach versus Leisure-only/revoked/outsider accounts, creation and duplicate validation, value retention, error-summary focus and linked field, 320 CSS-pixel layout, simulated 200% CSS zoom, keyboard tab order, pt-PT language and serious/critical axe violations. CSS zoom is **not** manual browser zoom or named screen-reader evidence. Native `autofocus` is exercised with the unchanged `script-src 'self'` CSP.
