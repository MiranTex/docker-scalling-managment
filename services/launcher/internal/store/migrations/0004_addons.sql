-- Addons: bases de dados (Postgres) e caches (Redis) de um ambiente. Cada
-- addon é um container próprio com volume próprio -- nada é partilhado com
-- outros ambientes ou projetos, nem com o Postgres da plataforma. A URL de
-- ligação vive em environment_secrets (managed_by = 'addon:<slug>'), com o
-- nome em env_var (ex: DATABASE_URL), e é injetada nos serviços do ambiente.
CREATE TABLE launcher.addons (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES launcher.environments(id) ON DELETE CASCADE,
    slug           TEXT NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('postgres', 'redis')),
    image          TEXT NOT NULL,
    env_var        TEXT NOT NULL,
    vcpu           NUMERIC(6,3) NOT NULL,
    memory_mb      BIGINT NOT NULL,
    -- Backups diários (só postgres): o agendador corre um pg_dump quando
    -- last_backup_at tem mais de 24h.
    backup_enabled BOOLEAN NOT NULL DEFAULT true,
    last_backup_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by     TEXT NOT NULL,
    UNIQUE (environment_id, slug),
    UNIQUE (environment_id, env_var)
);

-- Jobs: containers que correm até ao fim (backups, restores, e na fase
-- seguinte migrations e comandos manuais). Fica o exit code e o fim do
-- output, para se perceber depois porque é que algo falhou.
CREATE TABLE launcher.jobs (
    id             TEXT PRIMARY KEY,
    environment_id TEXT NOT NULL REFERENCES launcher.environments(id) ON DELETE CASCADE,
    addon_id       TEXT REFERENCES launcher.addons(id) ON DELETE SET NULL,
    -- Preenchidos por jobs de serviço (migrations, comandos manuais); a
    -- FK para launcher.services entra com essa tabela.
    service_id     TEXT,
    revision_id    TEXT,
    kind           TEXT NOT NULL,
    image          TEXT NOT NULL,
    cmd            JSONB NOT NULL DEFAULT '[]',
    status         TEXT NOT NULL,              -- 'running' | 'succeeded' | 'failed'
    exit_code      INTEGER,
    output         TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at    TIMESTAMPTZ,
    created_by     TEXT NOT NULL
);
CREATE INDEX jobs_environment_idx ON launcher.jobs (environment_id, created_at DESC);
