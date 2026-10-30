-- Proteção com utilizador e password (basic auth do Traefik) à frente de um
-- serviço web -- ex: mostrar uma fase da app a um cliente. Não é a
-- autenticação da app: é o Traefik que pede, antes de chegar às réplicas.
-- Só o hash bcrypt (o formato que o Traefik lê); vazio = sem proteção.
ALTER TABLE launcher.services
    ADD COLUMN basic_auth_user TEXT NOT NULL DEFAULT '',
    ADD COLUMN basic_auth_hash TEXT NOT NULL DEFAULT '';
