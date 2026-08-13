-- Uma linha por instância que o launcher criou -- container "solo" (uma
-- app qualquer, sem autoscaling) ou "group" (um autoscaler-group inteiro,
-- que depois gere as suas próprias réplicas sozinho). Nunca guardamos env
-- resolvido nem valores de segredo aqui: só a referência ao modelo
-- (template_name, ver services/templatesadmin) -- os segredos continuam a
-- viver só no secretsadmin, resolvidos em memória a cada lançamento (ver
-- internal/secretsclient).
CREATE TABLE launcher.instances (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL,              -- 'solo' | 'group'
    template_name TEXT NOT NULL,
    container_id  TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL,              -- 'running' | 'stopped' | 'failed'
    error         TEXT NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by    TEXT NOT NULL,
    stopped_at    TIMESTAMPTZ
);
