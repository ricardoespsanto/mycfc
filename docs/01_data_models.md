# Task: Generate PostgreSQL Schema & Safe Goose Migrations

Generate the database schema as a `pressly/goose` up/down migration file (e.g., `00001_init_schema.sql`) and the `queries.sql` file for `sqlc`. 

**Critical Constraint:** Migrations must be designed for zero-downtime deployments. Use `CREATE INDEX CONCURRENTLY` for indices. Use UUIDs (`uuid_generate_v4()`) for primary keys.

1. **Users**: id, name, email, password_hash, role (Enum), squad_category, guardian_id (self-referencing FK).
2. **Equipment**: id, name, type (Boat, Paddle, Vehicle), status (Operational, Maintenance).
3. **RepairRequests**: id, equipment_id, reported_by_id, issue_description, status, image_url (nullable text for S3 blob reference), date_reported.
4. **WhatsAppGroups**: id, name, discipline, target_role, url.
5. **Queries**: Write CRUD operations in `queries.sql`. Include a query fetching repair requests joined with equipment/users, and a query to update the `image_url` for a repair request.

Provide the SQL files and the `sqlc.yaml` configuration.
