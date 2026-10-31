-- Acesso público aos addons storage. public_endpoint põe o addon num
-- endereço público (Traefik, HTTPS): serve links assinados dos buckets
-- privados e os diretos dos públicos.
ALTER TABLE launcher.addons ADD COLUMN public_endpoint BOOLEAN NOT NULL DEFAULT false;

-- Os buckets de um addon storage: o "app" de sempre e os que se criarem.
-- public = leitura anónima dos objetos (CDN); cors_origins = de onde um
-- browser os pode usar.
CREATE TABLE launcher.storage_buckets (
    addon_id     TEXT NOT NULL REFERENCES launcher.addons(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    public       BOOLEAN NOT NULL DEFAULT false,
    cors_origins JSONB NOT NULL DEFAULT '[]',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by   TEXT NOT NULL,
    PRIMARY KEY (addon_id, name)
);
INSERT INTO launcher.storage_buckets (addon_id, name, created_by)
    SELECT id, 'app', created_by FROM launcher.addons WHERE kind = 'storage';

-- Um domínio próprio é de um serviço ou de um bucket (uma CDN:
-- cdn.loja.pt -> o bucket media).
ALTER TABLE launcher.domains ALTER COLUMN service_id DROP NOT NULL;
ALTER TABLE launcher.domains ADD COLUMN addon_id TEXT;
ALTER TABLE launcher.domains ADD COLUMN bucket TEXT;
ALTER TABLE launcher.domains ADD CONSTRAINT domains_bucket_fk
    FOREIGN KEY (addon_id, bucket) REFERENCES launcher.storage_buckets(addon_id, name) ON DELETE CASCADE;
ALTER TABLE launcher.domains ADD CONSTRAINT domains_one_owner
    CHECK ((service_id IS NULL) <> (addon_id IS NULL));
CREATE INDEX domains_bucket_idx ON launcher.domains (addon_id, bucket);
