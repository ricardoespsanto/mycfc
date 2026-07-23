-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE role AS ENUM ('Admin', 'Competitor', 'Leisure', 'Guardian');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE squad_category AS ENUM ('Iniciante', 'Polo_Senior', 'Master_A', 'Lazer', 'None');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE repair_status AS ENUM ('Pendente', 'Em_Analise', 'Resolvido');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE consent_type AS ENUM ('Termos_Gerais', 'Uso_Imagem', 'Responsabilidade_Menor');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE equipment_type AS ENUM ('Boat', 'Paddle', 'Vehicle');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE equipment_status AS ENUM ('Operational', 'Maintenance', 'Retired');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE maintenance_status AS ENUM ('Scheduled', 'In_Progress', 'Completed', 'Cancelled');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$ BEGIN
    CREATE TYPE metric_type AS ENUM ('Distance_Metres', 'Duration_Seconds', 'Sessions', 'Custom');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP TYPE IF EXISTS metric_type;
DROP TYPE IF EXISTS maintenance_status;
DROP TYPE IF EXISTS equipment_status;
DROP TYPE IF EXISTS equipment_type;
DROP TYPE IF EXISTS consent_type;
DROP TYPE IF EXISTS repair_status;
DROP TYPE IF EXISTS squad_category;
DROP TYPE IF EXISTS role;
DROP EXTENSION IF EXISTS pgcrypto;
DROP EXTENSION IF EXISTS citext;
