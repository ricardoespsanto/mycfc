# Slice 01 — production foundation

This slice establishes the contracts that later feature work must build on:

- pinned Go module and tool versions;
- production configuration validation with secret redaction;
- PostgreSQL schema, migrations, indexes, constraints, and sqlc query contracts;
- PostgreSQL-backed SCS sessions and Gorilla CSRF wiring;
- request IDs, trusted-proxy processing, security headers, panic recovery, and JSON access logs;
- liveness/readiness endpoints and graceful shutdown;
- private S3/MinIO object storage adapter;
- hostile repair-photo validation;
- deterministic frontend asset embedding;
- PostgreSQL/MinIO local bootstrap through Docker Compose;
- the complete HTTP route surface, with unfinished business routes returning explicit HTTP 501 responses.

The next slice should implement login, logout, adult registration, current-user loading, and role middleware as one end-to-end vertical flow.
