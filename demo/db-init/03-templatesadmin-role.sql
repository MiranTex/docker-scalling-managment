-- Provisiona a role Postgres que o services/templatesadmin usa para se
-- ligar ao Postgres partilhado (services/database) neste demo -- mesmo
-- raciocínio de 02-secretsadmin-role.sql: só a role e o GRANT CREATE
-- mínimo, templatesadmin cria o próprio schema ("service_templates")
-- sozinho no arranque (ver internal/store/migrate.go).
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'templatesadmin') THEN
    -- Senha de dev -- troque isto (e TEMPLATESADMIN_DATABASE_URL em
    -- demo/docker-compose.yml) antes de sair de uma máquina de dev.
    CREATE ROLE templatesadmin WITH LOGIN PASSWORD 'templatesadmin';
  END IF;
END
$$;

GRANT CREATE ON DATABASE app TO templatesadmin;
