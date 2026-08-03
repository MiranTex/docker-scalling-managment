-- email_verified_at é NULL até o dono provar que é dono do e-mail. Sem
-- isto, uma conta criada por senha nunca confirma que o e-mail pertence
-- mesmo a quem se registou -- o que abre uma janela de "account
-- squatting": alguém regista o e-mail de outra pessoa, e se essa pessoa
-- depois tentar entrar por login social (provider confirma o e-mail),
-- ficaria ligada à conta errada (ver oauth.Manager.Login, que agora usa
-- esta coluna para decidir se deve "reclamar" uma conta squatted em vez
-- de confiar cegamente nela).
ALTER TABLE auth.users ADD COLUMN email_verified_at TIMESTAMPTZ;

-- email_verifications: token opaco de uso único (mesmo padrão de
-- refresh_tokens e api_keys -- só o hash é guardado, nunca o segredo em
-- si). consumed_at marca quando foi usado; um token já consumido não
-- verifica nada de novo.
CREATE TABLE auth.email_verifications (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX email_verifications_user_id_idx ON auth.email_verifications (user_id);
