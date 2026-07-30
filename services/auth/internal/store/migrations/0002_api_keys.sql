-- api_keys: credenciais de longa duração para autenticação
-- máquina-a-máquina. "owner" é texto livre (não uma foreign key pra
-- users) de propósito: também serve para chaves M2M puras, atribuídas a
-- um nome de serviço, sem conta de utilizador nenhuma por trás.
--
-- scopes fica como TEXT (lista separada por vírgulas) em vez de um array
-- nativo do Postgres -- evita depender de um tipo de coluna que o
-- database/sql genérico não sabe (des)serializar sozinho sem uma
-- biblioteca extra (pq.Array ou equivalente).
CREATE TABLE auth.api_keys (
    id         UUID PRIMARY KEY,
    owner      TEXT NOT NULL,
    key_hash   TEXT NOT NULL UNIQUE,
    scopes     TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

-- Toda revogação e toda futura listagem "minhas chaves" busca por owner.
CREATE INDEX api_keys_owner_idx ON auth.api_keys (owner);
