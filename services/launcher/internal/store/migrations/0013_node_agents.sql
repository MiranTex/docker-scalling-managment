-- Nós remotos: entram com um token de uso único (node_join_tokens, criado
-- por um admin da plataforma) e ficam com uma credencial própria
-- (token_hash), com que o agente se liga ao launcher. Só se guarda o hash
-- de cada token.
ALTER TABLE launcher.nodes ADD COLUMN kind TEXT NOT NULL DEFAULT 'local'; -- local | agent
ALTER TABLE launcher.nodes ADD COLUMN token_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE launcher.nodes ADD COLUMN agent_version TEXT NOT NULL DEFAULT '';

CREATE TABLE launcher.node_join_tokens (
    token_hash   TEXT PRIMARY KEY,
    created_by   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    used_at      TIMESTAMPTZ,
    used_by_node TEXT
);
