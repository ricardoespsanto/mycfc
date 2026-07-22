# Foundation slice verification

Verified on the generation host:

- `gofmt -l` returned no files.
- All shell scripts passed `bash -n`.
- `npm ci`, `npm test`, and `npm run build` passed.
- The frontend build reproduced the fingerprinted CSS/JavaScript assets and manifest.
- JSON files and the generated asset manifest parsed successfully.
- The standard-library unit packages were executed in an isolated compatibility harness: handlers, HTTP middleware, localisation, and validation passed.
- Configuration tests were executed with a minimal local compatibility shim for `caarlos0/env`; they passed.
- Repair-photo tests were executed with a test-only WebP registration shim; the JPEG/PNG, hostile input, size, and dimension cases passed.
- Pure-package `go vet` checks passed in the same isolated harness.

Not executable on the generation host:

- The full `go test ./...` and `go vet ./...` commands, because the host cannot reach the Go module proxy and does not have the pinned Go 1.26.5 toolchain/module cache.
- `sqlc generate`/`sqlc compile`, because the pinned binary cannot be downloaded here.
- Goose migrations and integration tests, because Docker and PostgreSQL are unavailable here.
- Docker image and Compose validation, because Docker is unavailable here.
- Browser end-to-end tests, Terraform validation, and deployment checks; those implementation slices are deliberately not present yet.

Run this in a networked development environment with Docker:

```bash
cp .env.example .env
make tools
make dev-bootstrap
make verify-foundation
```

`make verify` is deliberately a red gate until all production slices are implemented.
