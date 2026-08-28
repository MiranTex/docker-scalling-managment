.PHONY: build-images migrations-status migrations-check

DEMO_COMPOSE ?= demo/docker-compose.yml
PGUSER ?= app
PGDATABASE ?= app

build-images:
	docker build -t autoscaler-group:latest services/autoscaler
	docker build -t auth-service:latest services/auth
	docker build -t portal:latest services/portal
	docker build -t database-service:latest services/database
	docker build -t secretsadmin:latest services/secretsadmin
	docker build -t templatesadmin:latest services/templatesadmin
	docker build -t launcher:latest services/launcher

migrations-status:
	@echo "Migration status (demo database)"
	@docker compose -f $(DEMO_COMPOSE) exec -T database \
		psql -U $(PGUSER) -d $(PGDATABASE) -v ON_ERROR_STOP=1 -c "SELECT 'auth' AS schema, version, applied_at FROM auth.schema_migrations ORDER BY version;"
	@docker compose -f $(DEMO_COMPOSE) exec -T database \
		psql -U $(PGUSER) -d $(PGDATABASE) -v ON_ERROR_STOP=1 -c "SELECT 'launcher' AS schema, version, applied_at FROM launcher.schema_migrations ORDER BY version;"
	@docker compose -f $(DEMO_COMPOSE) exec -T database \
		psql -U $(PGUSER) -d $(PGDATABASE) -v ON_ERROR_STOP=1 -c "SELECT 'secrets' AS schema, version, applied_at FROM secrets.schema_migrations ORDER BY version;"
	@docker compose -f $(DEMO_COMPOSE) exec -T database \
		psql -U $(PGUSER) -d $(PGDATABASE) -v ON_ERROR_STOP=1 -c "SELECT 'service_templates' AS schema, version, applied_at FROM service_templates.schema_migrations ORDER BY version;"

migrations-check:
	@set -e; \
	auth_expected=$$(find services/auth/internal/store/migrations -name '*.sql' | wc -l | tr -d ' '); \
	launcher_expected=$$(find services/launcher/internal/store/migrations -name '*.sql' | wc -l | tr -d ' '); \
	secrets_expected=$$(find services/secretsadmin/internal/store/migrations -name '*.sql' | wc -l | tr -d ' '); \
	templates_expected=$$(find services/templatesadmin/internal/store/migrations -name '*.sql' | wc -l | tr -d ' '); \
	auth_applied=$$(docker compose -f $(DEMO_COMPOSE) exec -T database psql -U $(PGUSER) -d $(PGDATABASE) -At -c "SELECT COUNT(*) FROM auth.schema_migrations;"); \
	launcher_applied=$$(docker compose -f $(DEMO_COMPOSE) exec -T database psql -U $(PGUSER) -d $(PGDATABASE) -At -c "SELECT COUNT(*) FROM launcher.schema_migrations;"); \
	secrets_applied=$$(docker compose -f $(DEMO_COMPOSE) exec -T database psql -U $(PGUSER) -d $(PGDATABASE) -At -c "SELECT COUNT(*) FROM secrets.schema_migrations;"); \
	templates_applied=$$(docker compose -f $(DEMO_COMPOSE) exec -T database psql -U $(PGUSER) -d $(PGDATABASE) -At -c "SELECT COUNT(*) FROM service_templates.schema_migrations;"); \
	echo "auth: expected=$$auth_expected applied=$$auth_applied"; \
	echo "launcher: expected=$$launcher_expected applied=$$launcher_applied"; \
	echo "secrets: expected=$$secrets_expected applied=$$secrets_applied"; \
	echo "service_templates: expected=$$templates_expected applied=$$templates_applied"; \
	if [ "$$auth_expected" -ne "$$auth_applied" ] || \
	   [ "$$launcher_expected" -ne "$$launcher_applied" ] || \
	   [ "$$secrets_expected" -ne "$$secrets_applied" ] || \
	   [ "$$templates_expected" -ne "$$templates_applied" ]; then \
		echo "ERROR: migration drift detected"; \
		exit 1; \
	fi; \
	echo "OK: all migrations applied";

start-demo:
	docker compose -f $(DEMO_COMPOSE) up -d

start-monitoring:
	docker compose -f services/monitoring/docker-compose.yml up -d