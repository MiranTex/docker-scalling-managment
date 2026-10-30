-- Segundo fator (TOTP). O segredo vai cifrado (AES-GCM, chave fora da
-- base: AUTH_MFA_KEY). confirmed_at a NULL = a ser configurado (a pessoa
-- ainda não confirmou com um código). last_step: o último passo de 30s
-- usado -- cada código serve uma vez.
CREATE TABLE auth.mfa_totp (
    user_id      UUID PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,
    secret_enc   TEXT NOT NULL,
    confirmed_at TIMESTAMPTZ,
    last_step    BIGINT NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Códigos de recuperação (só o hash), de uso único.
CREATE TABLE auth.mfa_recovery_codes (
    user_id   UUID NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL,
    used_at   TIMESTAMPTZ,
    PRIMARY KEY (user_id, code_hash)
);
