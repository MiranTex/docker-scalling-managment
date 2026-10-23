-- Tokens de deploy: credenciais de máquina (CI) com âmbito de UM projeto,
-- e opcionalmente de um só ambiente dele. Só servem para criar deploys e
-- ler o seu estado -- nunca para mexer em configuração, segredos ou dados.
-- Guarda-se só o SHA-256: o token em claro aparece uma vez, ao criar.
CREATE TABLE launcher.deploy_tokens (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES launcher.projects(id) ON DELETE CASCADE,
    environment_id TEXT REFERENCES launcher.environments(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    token_hash     TEXT NOT NULL UNIQUE,
    -- Primeiros caracteres do token, para o reconhecer numa lista.
    prefix         TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by     TEXT NOT NULL,
    last_used_at   TIMESTAMPTZ,
    revoked_at     TIMESTAMPTZ
);
CREATE INDEX deploy_tokens_project_idx ON launcher.deploy_tokens (project_id);
