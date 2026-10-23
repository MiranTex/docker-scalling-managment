-- Serviços de um ambiente e o seu histórico de deploys.
--
-- O serviço guarda a configuração "para o próximo deploy" (cmd, env,
-- recursos, porta, health check) e o scaling (que se aplica logo). Cada
-- deploy congela essa configuração mais a imagem numa REVISÃO imutável;
-- as réplicas a correr são sempre de uma revisão, e um rollback é só
-- voltar a pôr uma revisão anterior como atual.
CREATE TABLE launcher.services (
    id                     TEXT PRIMARY KEY,
    environment_id         TEXT NOT NULL REFERENCES launcher.environments(id) ON DELETE CASCADE,
    slug                   TEXT NOT NULL,
    kind                   TEXT NOT NULL CHECK (kind IN ('web', 'worker')),
    port                   INTEGER NOT NULL DEFAULT 0,
    health_path            TEXT NOT NULL DEFAULT '',
    health_timeout_seconds INTEGER NOT NULL DEFAULT 120,
    cmd                    JSONB NOT NULL DEFAULT '[]',
    env                    JSONB NOT NULL DEFAULT '[]',
    release_cmd            JSONB NOT NULL DEFAULT '[]',
    vcpu                   NUMERIC(6,3) NOT NULL,
    memory_mb              BIGINT NOT NULL,
    scaling_mode           TEXT NOT NULL DEFAULT 'fixed' CHECK (scaling_mode IN ('fixed', 'auto')),
    replicas               INTEGER NOT NULL DEFAULT 1,
    min_replicas           INTEGER NOT NULL DEFAULT 1,
    max_replicas           INTEGER NOT NULL DEFAULT 3,
    cpu_scale_up_percent   DOUBLE PRECISION NOT NULL DEFAULT 70,
    cpu_scale_down_percent DOUBLE PRECISION NOT NULL DEFAULT 20,
    desired_state          TEXT NOT NULL DEFAULT 'running' CHECK (desired_state IN ('running', 'stopped')),
    current_revision_id    TEXT,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by             TEXT NOT NULL,
    UNIQUE (environment_id, slug)
);

CREATE TABLE launcher.revisions (
    id                     TEXT PRIMARY KEY,
    service_id             TEXT NOT NULL REFERENCES launcher.services(id) ON DELETE CASCADE,
    number                 INTEGER NOT NULL,
    image                  TEXT NOT NULL,
    cmd                    JSONB NOT NULL DEFAULT '[]',
    env                    JSONB NOT NULL DEFAULT '[]',
    release_cmd            JSONB NOT NULL DEFAULT '[]',
    port                   INTEGER NOT NULL DEFAULT 0,
    health_path            TEXT NOT NULL DEFAULT '',
    health_timeout_seconds INTEGER NOT NULL DEFAULT 120,
    vcpu                   NUMERIC(6,3) NOT NULL,
    memory_mb              BIGINT NOT NULL,
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by             TEXT NOT NULL,
    UNIQUE (service_id, number)
);

ALTER TABLE launcher.services
    ADD CONSTRAINT services_current_revision_fk
    FOREIGN KEY (current_revision_id) REFERENCES launcher.revisions(id) ON DELETE SET NULL;

CREATE TABLE launcher.deployments (
    id           TEXT PRIMARY KEY,
    service_id   TEXT NOT NULL REFERENCES launcher.services(id) ON DELETE CASCADE,
    revision_id  TEXT NOT NULL REFERENCES launcher.revisions(id) ON DELETE CASCADE,
    -- pending -> pulling -> releasing -> starting -> switching -> succeeded
    -- (ou failed em qualquer passo; a revisão anterior fica intacta).
    status       TEXT NOT NULL,
    error        TEXT NOT NULL DEFAULT '',
    log          TEXT NOT NULL DEFAULT '',
    triggered_by TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at  TIMESTAMPTZ
);
CREATE INDEX deployments_service_idx ON launcher.deployments (service_id, created_at DESC);

-- Credenciais de registry privado por projeto, usadas no pull das imagens
-- de deploy. A password vai cifrada, como os segredos.
CREATE TABLE launcher.registry_credentials (
    project_id TEXT NOT NULL REFERENCES launcher.projects(id) ON DELETE CASCADE,
    registry   TEXT NOT NULL,
    username   TEXT NOT NULL,
    ciphertext BYTEA NOT NULL,
    nonce      BYTEA NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT NOT NULL,
    PRIMARY KEY (project_id, registry)
);

ALTER TABLE launcher.jobs
    ADD CONSTRAINT jobs_service_fk FOREIGN KEY (service_id) REFERENCES launcher.services(id) ON DELETE SET NULL,
    ADD CONSTRAINT jobs_revision_fk FOREIGN KEY (revision_id) REFERENCES launcher.revisions(id) ON DELETE SET NULL;
