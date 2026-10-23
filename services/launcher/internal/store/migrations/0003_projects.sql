-- Modelo de projetos: uma conta tem projetos, um projeto tem ambientes
-- (production, staging...), e cada ambiente é uma fronteira de isolamento
-- real -- rede Docker própria, segredos próprios, e mais tarde serviços e
-- addons próprios. Por agora só existe a conta "default" (a plataforma não
-- é distribuída como SaaS), mas account_id já existe para não ter de
-- migrar tudo quando houver mais.
CREATE TABLE launcher.accounts (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO launcher.accounts (id, name) VALUES ('default', 'Default');

CREATE TABLE launcher.projects (
    id              TEXT PRIMARY KEY,
    account_id      TEXT NOT NULL REFERENCES launcher.accounts(id),
    slug            TEXT NOT NULL,
    name            TEXT NOT NULL,
    -- Teto de recursos de TODOS os containers do projeto somados. Zero =
    -- sem teto (só o do host, ver capacity.go).
    quota_vcpu      NUMERIC(8,3) NOT NULL DEFAULT 0,
    quota_memory_mb BIGINT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by      TEXT NOT NULL,
    UNIQUE (account_id, slug)
);

CREATE TABLE launcher.environments (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES launcher.projects(id) ON DELETE CASCADE,
    slug       TEXT NOT NULL,
    -- Nome da rede Docker deste ambiente (bs-<projeto>-<ambiente>). Guardado
    -- e não recalculado, para renomear um slug nunca "perder" a rede.
    network    TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by TEXT NOT NULL,
    UNIQUE (project_id, slug)
);

-- Segredos de um ambiente, cifrados em repouso (AES-256-GCM com
-- LAUNCHER_SECRETS_KEY). Ao contrário do secretsadmin (global, para o
-- fluxo antigo de modelos), estes só são visíveis aos containers deste
-- ambiente, via ${secret:NOME} no env de um serviço. managed_by preenchido
-- = criado pela plataforma (ex: credenciais de um addon), não editável à mão.
CREATE TABLE launcher.environment_secrets (
    environment_id TEXT NOT NULL REFERENCES launcher.environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    ciphertext     BYTEA NOT NULL,
    nonce          BYTEA NOT NULL,
    managed_by     TEXT NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by     TEXT NOT NULL,
    PRIMARY KEY (environment_id, name)
);
