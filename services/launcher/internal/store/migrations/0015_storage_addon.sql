-- Addon "storage": armazenamento de ficheiros com a API do S3 (SeaweedFS).
-- O env_var de um storage é o prefixo das variáveis (STORAGE -> STORAGE_URL,
-- STORAGE_BUCKET...).
ALTER TABLE launcher.addons DROP CONSTRAINT IF EXISTS addons_kind_check;
ALTER TABLE launcher.addons ADD CONSTRAINT addons_kind_check CHECK (kind IN ('postgres', 'redis', 'storage'));
