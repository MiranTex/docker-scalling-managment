.PHONY: cli cli-linux install-cli basestack install-basestack build-images migrations-status migrations-check start-demo demo-down start-monitoring db-upgrade-pg17

DEMO_COMPOSE ?= demo/docker-compose.yml
PGUSER ?= app
PGDATABASE ?= app

# cli compila a CLI da plataforma (bs) para esta máquina, em bin/bs --
# ver services/cli/README.md.
cli:
	cd services/cli && go build -o ../../bin/bs ./cmd/bs

# cli-linux compila o bs para servidores Linux (Intel/AMD e ARM), em bin/.
# Copia o que servir (uname -m: x86_64 -> amd64, aarch64 -> arm64).
cli-linux:
	cd services/cli && GOOS=linux GOARCH=amd64 go build -o ../../bin/bs-linux-amd64 ./cmd/bs
	cd services/cli && GOOS=linux GOARCH=arm64 go build -o ../../bin/bs-linux-arm64 ./cmd/bs

# install-cli põe o bs no PATH (por defeito /usr/local/bin; usa sudo só se
# não der para escrever lá). Outro sítio: make install-cli PREFIX=~/.local
PREFIX ?= /usr/local
install-cli: cli
	@mkdir -p "$(PREFIX)/bin" 2>/dev/null || sudo mkdir -p "$(PREFIX)/bin"
	@if [ -w "$(PREFIX)/bin" ]; then install -m 0755 bin/bs "$(PREFIX)/bin/bs"; \
	else echo "A instalar em $(PREFIX)/bin com sudo"; sudo install -m 0755 bin/bs "$(PREFIX)/bin/bs"; fi
	@echo "bs instalado em $(PREFIX)/bin/bs -- experimenta: bs projects"

# basestack instala e gere a plataforma numa máquina (ver
# services/cli/README.md); é para quem opera, o bs para quem usa.
basestack:
	cd services/cli && go build -o ../../bin/basestack ./cmd/basestack

install-basestack: basestack
	@if [ -w "$(PREFIX)/bin" ]; then install -m 0755 bin/basestack "$(PREFIX)/bin/basestack"; \
	else echo "A instalar em $(PREFIX)/bin com sudo"; sudo install -m 0755 bin/basestack "$(PREFIX)/bin/basestack"; fi
	@echo "basestack instalado em $(PREFIX)/bin/basestack -- experimenta: basestack doctor"

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

# "compose down" só derruba a plataforma: groups, instâncias e réplicas
# lançados pelo launcher sobrevivem de propósito (ver "Derrubar" em
# demo/README.md). Groups param primeiro para nenhum repor réplicas a meio.
# observability-net fica: pertence a services/monitoring.
demo-down:
	docker compose -f $(DEMO_COMPOSE) down
	@ids=$$(docker ps -q --filter label=autoscaler.role=group); \
	if [ -n "$$ids" ]; then docker stop $$ids >/dev/null; fi
	@ids=$$(docker ps -aq --filter label=base-stack.managed=true); \
	if [ -n "$$ids" ]; then docker stop $$ids >/dev/null; docker rm $$ids; fi
	@for net in $$(docker network ls --filter label=base-stack.managed=true --format '{{.Name}}'); do \
		[ "$$net" = observability-net ] || docker network rm "$$net" >/dev/null || true; \
	done

start-monitoring:
	docker compose -f services/monitoring/docker-compose.yml up -d
# Copia os dados do Postgres 16 antigo (volume demo_demo-database-data,
# que fica intacto) para o novo cluster 17 do demo. Corre-se UMA vez, com
# o demo em baixo: sobe um Postgres 16 temporário sobre o volume antigo
# (sem archive_mode, porque o pgBackRest não existe nessa imagem), sobe
# só o "database" novo, e passa tudo com pg_dumpall. Erros como "role ...
# already exists", "database app already exists" ou "is being accessed by
# other users" são esperados e inofensivos: o cluster novo já criou as
# roles e a base (e o dbadmin está ligado a ela), e os dados entram dentro
# delas na mesma.
OLD_DB_VOLUME ?= demo_demo-database-data
db-upgrade-pg17:
	@docker volume inspect $(OLD_DB_VOLUME) >/dev/null 2>&1 || { echo "volume $(OLD_DB_VOLUME) não existe -- nada para migrar"; exit 1; }
	docker run -d --rm --name bs-pg16-upgrade -v $(OLD_DB_VOLUME):/var/lib/postgresql/data postgres:16-bookworm postgres -c archive_mode=off
	@until docker exec bs-pg16-upgrade pg_isready -U $(PGUSER) >/dev/null 2>&1; do sleep 1; done
	docker compose -f $(DEMO_COMPOSE) up -d --wait database
	docker exec bs-pg16-upgrade pg_dumpall -U $(PGUSER) --clean --if-exists \
		| docker compose -f $(DEMO_COMPOSE) exec -T database psql -q -U $(PGUSER) -d postgres >/dev/null
	docker stop bs-pg16-upgrade
	@echo "OK: dados copiados para o Postgres 17. Sobe o resto com make start-demo."
