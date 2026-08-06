-- Uma linha por segredo: só o valor cifrado (AES-256-GCM, ver
-- internal/crypto) e o nonce usado nessa cifra ficam na base de dados --
-- o valor em claro nunca é persistido. name é a chave usada nas
-- referências ${secret:NAME} dentro dos launch templates do autoscaler
-- (ver services/autoscaler/internal/secretsclient).
CREATE TABLE secrets.secrets (
    name       TEXT PRIMARY KEY,
    ciphertext BYTEA NOT NULL,
    nonce      BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by TEXT NOT NULL
);
