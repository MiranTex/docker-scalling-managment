-- Provisiona a role Postgres que o services/launcher usa para se ligar
-- ao Postgres partilhado (services/database) neste demo -- mesmo
-- raciocínio de 03-templatesadmin-role.sql: só a role e o GRANT CREATE
-- mínimo, o launcher cria o próprio schema ("launcher") sozinho no
-- arranque (ver internal/store/migrate.go).
DO $$
BEGIN
  IF NOT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = 'launcher') THEN
    -- Senha de dev -- troque isto (e LAUNCHER_DATABASE_URL em
    -- demo/docker-compose.yml) antes de sair de uma máquina de dev.
    CREATE ROLE launcher WITH LOGIN PASSWORD 'launcher';
  END IF;
END
$$;

GRANT CREATE ON DATABASE app TO launcher;
