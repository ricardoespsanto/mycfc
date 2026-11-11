# Announcement audience #339 isolated browser evidence

Run only against a reviewed backend commit; the runner exports that exact commit to external evidence and builds it there. It never reads `.env`, resets shared services, or invokes the shared E2E/integration runners.

```sh
python e2e/run-announcement-audience-339.py \
  /tmp/announcement-339-evidence-UNIQUE \
  /absolute/path/to/reviewed/backend-checkout \
  b5ed711638cc978d334b971b11887d4899fb4108
```

Requires Docker, Go, Node/npm and Python with `tarfile` extraction filters. The external directory must not exist; it is created mode 0700 and contains synthetic session cookies (0600), logs, source/test SHA-256 manifests, binary SHA-256, Playwright JSON/trace/screenshot artifacts, preserved receipt and historical-row comparisons. Dependencies use the repository lockfile in an external prefix; selected tests/config are copied beside those dependencies, with no checkout mount or mutation. Runtime image identities and isolated resource names/ports are recorded. Outside this opt-in fixture the specs are skipped; the selected isolated run must have no skips. Chromium runs in the repository-pinned `mcr.microsoft.com/playwright:v1.63.0-noble` container with one worker and zero retries; the runner requires exactly four passed selected cases and rejects skips/retries.

The uniquely named disposable PostgreSQL container exposes only a random loopback port and installs the complete baseline and migration ledger. Synthetic guardian activation follows the existing crew fixture's isolated test policy pattern; it is not real policy approval or live verification. The statically built app runs in a separate disposable Alpine container sharing the database network namespace; both PostgreSQL and app published ports bind to 127.0.0.1. The `finally` block removes only its named app, browser and database containers, and writes `cleanup.json` with process/container/port checks. Evidence remains for independent review; delete that exact directory manually when no longer needed.

Coverage exercises actual authenticated classification sport/craft forms, all six legacy craft target UUIDs resolved by code plus SUP, the current-participation prerequisite, stale legacy membership selections, meaningful programme/SUP OR with deduplication, deterministic eight-notice six-item pagination/panel versus full unread count, detail-only read marking, byte-for-byte historical delivery/read timestamp preservation through hide/re-add, Canoagem craft cascade and non-resurrection. Guardian tests distinguish verified child's K2 from guardian's own SUP and an inactive child's SUP; revoked authority is applied between phases while the original authenticated synthetic cookies are retained. Subsequent requests must lose child-only list/panel/detail/count eligibility while own sporting/OR eligibility remains. Panel checks navigate anew because the existing cache lasts for the page lifetime.

Keyboard Enter/Escape, close-button focus/return, visible trigger outline, 320px overflow and serious/critical axe checks cover the list and open panel. These automated checks do not establish manual screen-reader, real browser zoom, push invalidation, full repository verification, backend exhaustive matrix, independent QA approval, or production readiness.

Execution results and any failed setup/development attempts are recorded by the delivery worker alongside the final evidence path; a missing summary is a failed or incomplete run, never a pass.

Verified 11 November 2026 against backend checkpoint `b5ed711638cc978d334b971b11887d4899fb4108`: external evidence `/tmp/announcement-339-browser-evidence-final-optin` records **4 passed, 0 failed, 0 skipped, 0 retries**, one worker, repository-pinned Playwright 1.63.0 Chromium container; serious/critical axe checks passed on the 320px list and open bell. `summary.json` confirms historical rows equal and receipt preserved; `cleanup.json` confirms app stopped, no named containers remain, and ports 44451/49393 closed. Focused ESLint, Node syntax checks, Python AST parsing and staged diff whitespace checks also passed.

Earlier development evidence remains external and is not counted as final acceptance: attempts `-1` through `-3` failed on fixture deactivation ordering, PostgreSQL temporary-startup readiness and the nested read-only mount respectively; attempt `-4` had one failed correction status assertion and two passed browser cases. Those harness/assertion issues were corrected; attempt `-5` and `-final` each passed all four cases before the final historical-receipt and opt-in versions were settled. Independent QA and manual accessibility evidence remain outstanding.
