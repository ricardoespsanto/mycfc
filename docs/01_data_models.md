# Task: Generate PostgreSQL 16 Schema & Goose Migrations

Generate the `pressly/goose` migration (`00001_init.sql`) and `queries.sql` for `sqlc`. 
**Critical:** Use UUIDs (`uuid_generate_v4()`) for PKs. Use `timestamptz` for all date/time columns. Implement strict `ON DELETE CASCADE` (for consent forms) and `ON DELETE SET NULL` (for users on repair requests) to prevent orphaned data.

**Explicit Enums (Do not invent others):**
* `role`: 'Admin', 'Competitor', 'Leisure', 'Guardian'
* `squad_category`: 'Iniciante', 'Polo_Senior', 'Master_A', 'Lazer', 'None'
* `repair_status`: 'Pendente', 'Em_Analise', 'Resolvido'
* `consent_type`: 'Termos_Gerais', 'Uso_Imagem', 'Responsabilidade_Menor'

**Tables:**
1. **Users**: id, name, email, password_hash, role, squad_category, guardian_id (nullable, self-referencing FK).
2. **Equipment**: id, name, type (Boat, Paddle, Vehicle), status (Operational, Maintenance).
3. **RepairRequests**: id, equipment_id, reported_by_id (FK, SET NULL), issue_description, status (repair_status), image_url, date_reported.
4. **ConsentForms**: id, user_id (FK, CASCADE), consent_type, is_accepted (Boolean), date_signed.
5. **WhatsAppGroups**: id, name, discipline, target_role, url.

**sqlc Configuration:**
Ensure `sqlc.yaml` is configured to use the `pgx/v5` driver.
