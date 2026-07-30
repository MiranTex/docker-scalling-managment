-- oauth_identities: liga uma conta (auth.users) a uma identidade emitida
-- por um provider externo (Google, GitHub, ...). Uma conta pode ter várias
-- (senha + Google + GitHub, todas a mesma pessoa) -- é isso que permite
-- login social ficar ligado à MESMA conta que já existe com email+senha,
-- em vez de criar uma conta duplicada por método de login.
CREATE TABLE auth.oauth_identities (
    id               UUID PRIMARY KEY,
    user_id          UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    email            TEXT NOT NULL, -- email reportado pelo provider no momento da ligação, para auditoria
    linked_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_user_id)
);

CREATE INDEX oauth_identities_user_id_idx ON auth.oauth_identities (user_id);
