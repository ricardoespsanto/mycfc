ALTER TYPE consent_type ADD VALUE IF NOT EXISTS 'Dados_Saude';
ALTER TYPE consent_type ADD VALUE IF NOT EXISTS 'Foto_Perfil';
ALTER TABLE consent_forms DROP CONSTRAINT IF EXISTS consent_version_unique;
CREATE INDEX IF NOT EXISTS consent_user_type_version_idx ON consent_forms (user_id, consent_type, document_version, document_sha256, date_signed DESC);
