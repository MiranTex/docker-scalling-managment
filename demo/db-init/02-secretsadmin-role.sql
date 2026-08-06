-- Provisiona a role Postgres que o services/secretsadmin usa para se
-- ligar ao Postgres partilhado (services/database) neste demo -- mesmo
-- raciocínio de 01-auth-role.sql: só a role e o GRANT CREATE mínimo,
-- secretsadmin cria o próprio schema ("secrets") sozinho no arranque
-- (ver internal/store/migrate.go).
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'secretsadmin') THEN
    -- Senha de dev -- troque isto (e SECRETSADMIN_DATABASE_URL em
    -- demo/docker-compose.yml) antes de sair de uma máquina de dev.
    CREATE ROLE secretsadmin WITH LOGIN PASSWORD 'secretsadmin';
  END IF;
END
$$;

GRANT CREATE ON DATABASE app TO secretsadmin;
