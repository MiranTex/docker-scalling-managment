-- users: uma linha por conta. O e-mail é único e obrigatório mesmo para
-- contas que só vão usar OAuth/passkey no futuro -- é a chave de account
-- linking (mesma pessoa, vários métodos de login).
CREATE TABLE auth.users (
    id         UUID PRIMARY KEY,
    email      TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- password_credentials: separado de users porque nem toda conta tem senha
-- (uma conta pode existir só com OAuth/passkey, adicionado numa fase
-- futura). 1:1 com users -- user_id é a própria chave primária.
CREATE TABLE auth.password_credentials (
    user_id       UUID PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,
    password_hash TEXT NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- refresh_tokens: nunca guarda o segredo em si, só o hash (token_hash).
-- family_id agrupa todas as rotações nascidas do mesmo login -- é o que
-- permite revogar a "sessão" inteira de uma vez quando um reuso é
-- detectado (ver internal/refresh).
CREATE TABLE auth.refresh_tokens (
    id         UUID PRIMARY KEY,
    user_id    UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    family_id  UUID NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Toda rotação/deteção de reuso busca por family_id (revogar a família
-- inteira) -- sem índice, isso seria um table scan a cada rotação.
CREATE INDEX refresh_tokens_family_id_idx ON auth.refresh_tokens (family_id);
