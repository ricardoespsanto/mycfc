# Task: Local Development Environment (Docker Compose)

Create the necessary files for local development parity, ensuring developers can test S3 uploads and database migrations locally.

1. **docker-compose.yml**: 
   * Define a `postgres:16-alpine` service with a persistent volume (`pgdata`).
   * Define a `minio/minio` service to emulate AWS S3 locally. Expose port `9000` and `9001` (console). Configure it to auto-create the local bucket.
2. **Makefile**: Write a `Makefile` with commands:
   * `make dev-infra`: Starts postgres and minio.
   * `make migrate-up`: Runs local `goose` migrations.
   * `make dev`: Runs `templ generate --watch`, `sqlc generate`, and starts the Go server.
