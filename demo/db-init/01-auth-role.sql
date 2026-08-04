-- Provisiona a role Postgres que o services/auth usa para se ligar ao
-- Postgres partilhado (services/database) neste demo.
--
-- Ficheiros aqui dentro só correm UMA VEZ, na primeira inicialização de
-- um PGDATA vazio (é o comportamento padrão da imagem oficial do
-- Postgres para /docker-entrypoint-initdb.d/*.sql -- a nossa imagem
-- custom em services/database herda isso, não muda nada). Não é
-- reaplicado em reinícios normais do container.
--
-- Só cria a role e concede o mínimo que o próprio authd precisa pra se
-- virar sozinho a partir daqui: `store.Migrate()`
-- (services/auth/internal/store/migrate.go) faz
-- `CREATE SCHEMA IF NOT EXISTS auth` no arranque -- desde o Postgres 15,
-- CREATE numa database já não é concedido a PUBLIC por default, então
-- sem este GRANT explícito essa chamada falharia com "permission
-- denied for database". Tudo o resto (tabelas, índices) o próprio authd
-- cria dentro do schema "auth" assim que tiver esse privilégio -- este
-- serviço de banco de dados não precisa saber nada sobre o schema de
-- ninguém.
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'auth') THEN
    -- Senha de dev, igual à usada em services/auth/docker-compose.yml --
    -- troque isto (e AUTH_DATABASE_URL em launch-template.auth.json)
    -- antes de sair de uma máquina de dev.
    CREATE ROLE auth WITH LOGIN PASSWORD 'auth';
  END IF;
END
$$;

GRANT CREATE ON DATABASE app TO auth;
