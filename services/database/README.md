# database

Postgres partilhado do base-stack: um servidor Postgres com um schema por
serviço, em vez de cada serviço subir o seu próprio (ver
`services/auth/docker-compose.yml`, que ainda faz isso e já anuncia essa
migração). Este serviço garante duas coisas que um `postgres:16-alpine`
puro não dá de graça: **backups em rotina com PITR** (pgBackRest) e um
**modo dev para inspecionar dados** (Adminer), sem precisar instalar
cliente nenhum na tua máquina.

## Como funciona

- Imagem custom (`Dockerfile`) = Postgres oficial + `pgbackrest` + `cron`
  no mesmo container. `entrypoint-wrapper.sh` roda antes do Postgres:
  gera a config do pgBackRest a partir de variáveis de ambiente, agenda
  os backups no cron, e só depois entrega o controlo ao `dbadmin`
  (ver abaixo), que é quem efetivamente arranca o
  `docker-entrypoint.sh postgres` original, já com `-c archive_command=...`
  ligado -- é isso que arquiva o WAL continuamente e torna PITR possível
  (sem arquivamento contínuo, só dava pra restaurar exatamente no
  instante de um backup, nunca entre dois).
- O Postgres NÃO é o PID 1 deste container -- é o `dbadmin` (ver
  "API de administração" abaixo), que o arranca como seu processo filho.
  É o que permite ao `dbadmin` parar e voltar a arrancar o Postgres (para
  um PITR disparado pela API) sem derrubar o container inteiro a meio da
  operação.
- `ensure-stanza.sh` corre em background no arranque: espera o Postgres
  responder e cria a stanza do pgBackRest se ainda não existir (idempotente
  -- não recria numa reinicialização normal do container).
- `run-backup.sh full|diff|incr` é o comando que o cron chama sozinho
  (agendamento em `BACKUP_SCHEDULE_FULL`/`BACKUP_SCHEDULE_INCR`) e que tu
  também podes chamar manualmente.
- `restore-pitr.sh` é manual, nunca automático -- ver "Backups e PITR"
  abaixo.

## Configuração (variáveis de ambiente)

| Variável | Default | Descrição |
|---|---|---|
| `POSTGRES_DB` / `POSTGRES_USER` / `POSTGRES_PASSWORD` | `app` / `app` / `app` | Padrão da imagem oficial do Postgres. Senha de dev -- troque antes de sair de uma máquina de dev. |
| `PGBACKREST_STANZA` | `shared` | Nome da stanza pgBackRest (um cluster Postgres = uma stanza). |
| `PGBACKREST_REPO_TYPE` | `posix` | `posix` (volume local) ou `s3` (qualquer storage S3-compatible, incluindo MinIO). |
| `PGBACKREST_REPO_PATH` | `/var/lib/pgbackrest` | Só usado quando `PGBACKREST_REPO_TYPE=posix`. |
| `PGBACKREST_S3_BUCKET` / `_ENDPOINT` / `_REGION` / `_KEY` / `_KEY_SECRET` | *(obrigatórias se `PGBACKREST_REPO_TYPE=s3`)* | Credenciais/endereço do bucket. Para MinIO, `_ENDPOINT` é o host:porta do MinIO. |
| `PGBACKREST_S3_VERIFY_TLS` | `y` | Usa `n` só se for MinIO local com TLS auto-assinado (nunca em produção). |
| `PGBACKREST_RETENTION_FULL` | `2` | Quantos backups full manter (os incrementais/diff de um full expirado são removidos junto). |
| `PGBACKREST_PROCESS_MAX` | `1` | Paralelismo do backup/restore. Sobe se tiveres CPU sobrando. |
| `BACKUP_SCHEDULE_FULL` | `0 2 * * 0` | Cron (UTC) do backup full -- default domingo 02:00. |
| `BACKUP_SCHEDULE_INCR` | `0 2 * * 1-6` | Cron (UTC) do backup incremental -- default todo dia menos domingo, 02:00. |
| `DBADMIN_LISTEN_ADDR` | `:8091` | Porta interna da API de administração (`dbadmin`, ver abaixo). Nunca publique esta porta no host em produção. |
| `AUTH_SERVICE_URL` | `http://localhost:8081` | Onde o `dbadmin` busca as chaves públicas (JWKS) para validar quem chama a API. |
| `AUTH_ISSUER` / `AUTH_AUDIENCE` | `auth-service` / `base-stack` | Têm de bater exatamente com os mesmos valores configurados no `services/auth` que emite os tokens. |

## API de administração (dbadmin)

`dbadmin` (ver `dbadmin/`) é o PID 1 deste container -- expõe uma API
HTTP interna para o portal gerir esta base de dados sem precisar de
`docker exec`: estado do Postgres (versão, tamanho, ligações), histórico
de backups, disparar um backup manual, e PITR (parar o Postgres, restaurar
via pgBackRest, e arrancá-lo de novo em recovery pausado à espera de
confirmação). Protegida por access token do `services/auth` — só aceita
chamadas cuja claim `role` seja `infra-admin` (ver
`dbadmin/internal/httpapi`). Comandos de manutenção (VACUUM, terminar
ligações, ...) ficam para uma fase seguinte.

```sh
curl -H "Authorization: Bearer $TOKEN" http://localhost:8091/v1/status
curl -H "Authorization: Bearer $TOKEN" http://localhost:8091/v1/backups
curl -X POST -H "Authorization: Bearer $TOKEN" -d '{"type":"full"}' http://localhost:8091/v1/backups

# PITR -- ver aviso em "Backups e PITR" abaixo, isto é tão destrutivo
# quanto o restore-pitr.sh manual, só que sem o prompt interativo (a
# confirmação acontece na UI do portal antes deste pedido existir).
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -d '{"type":"time","target":"2026-08-04 10:00:00"}' \
  http://localhost:8091/v1/restore
# {"id":"restore-1","status":"running",...}
curl -H "Authorization: Bearer $TOKEN" http://localhost:8091/v1/restore/jobs/restore-1
# depois de "status":"awaiting_confirmation", inspeciona os dados e só
# depois confirma (isto promove -- ver aviso sobre timeline nova abaixo):
curl -X POST -H "Authorization: Bearer $TOKEN" http://localhost:8091/v1/restore/jobs/restore-1/confirm
```

Não há endpoint de "cancelar" um restore a meio -- uma vez que
`pgbackrest restore` já reescreveu o PGDATA, a única forma de "desistir"
é fazer outro restore, não existe um desfazer seguro nesse ponto.

No portal, isto aparece em `/admin/database` (ver `services/portal`).

## Subir

```sh
docker build -t database-service:latest services/database
docker compose -f services/database/docker-compose.yml up -d
```

Espera ficar `healthy` e confirma que a stanza foi criada:
```sh
docker compose -f services/database/docker-compose.yml logs postgres | grep ensure-stanza
docker compose -f services/database/docker-compose.yml exec postgres pgbackrest info --stanza=shared
```

## Modo dev: Adminer

Sobe junto no mesmo compose, em `http://localhost:8082`. Liga com:
- Sistema: `PostgreSQL`
- Servidor: `postgres` (já vem pré-preenchido via `ADMINER_DEFAULT_SERVER`)
- Utilizador/senha/BD: os valores de `POSTGRES_USER`/`POSTGRES_PASSWORD`/`POSTGRES_DB`

**Nunca exponhas a porta do Adminer fora da tua máquina/rede de
confiança** -- ele não tem rate-limit nem MFA próprios, é só uma camada
fina sobre o Postgres.

## Backups e PITR

### Backup manual

```sh
docker compose -f services/database/docker-compose.yml exec postgres run-backup.sh full
# ou: incr / diff
```

### Backup agendado

Já acontece sozinho via cron dentro do container (ver
`BACKUP_SCHEDULE_FULL`/`BACKUP_SCHEDULE_INCR`). Confirma que rodou:
```sh
docker compose -f services/database/docker-compose.yml exec postgres pgbackrest info --stanza=shared
```

### Restaurar para um instante específico (PITR)

Isto sobrescreve o diretório de dados -- **para o Postgres primeiro**:

```sh
docker compose -f services/database/docker-compose.yml stop postgres

docker compose -f services/database/docker-compose.yml run --rm \
  --entrypoint restore-pitr.sh postgres \
  --type=time --target="2026-08-04 10:00:00"

docker compose -f services/database/docker-compose.yml up -d postgres
docker compose -f services/database/docker-compose.yml logs -f postgres  # acompanha o recovery
```

O Postgres aplica o WAL até o instante pedido e depois **pausa antes de
confirmar** (`recovery_target_action` default é `pause`, não `promote`)
-- é uma proteção do próprio Postgres: dá pra inspecionar os dados em
modo read-only antes de decidir se é mesmo aquele o ponto certo. Confirma
que os dados estão como esperas e só depois promove:
```sh
docker compose -f services/database/docker-compose.yml exec -u postgres postgres \
  psql -U app -d app -c "SELECT pg_wal_replay_resume();"
```

**Cuidado ao "inspecionar os dados" enquanto pausado**: se o alvo caiu
*antes do commit* de uma transação que fazia DDL (ex: um `DROP TABLE`),
o lock exclusivo dessa transação continua efetivamente retido enquanto
o recovery está pausado -- um `SELECT` justamente nessa tabela **bloqueia
indefinidamente** à espera desse lock (viu-se isto na prática: a query
só destrancou depois do `pg_wal_replay_resume()`). `pg_wal_replay_resume()`
em si nunca bloqueia (não toca em nenhuma tabela) -- se um `SELECT` ficar
pendurado logo depois do restore, chama já o resume em vez de esperar a
consulta responder.

Sem `--type=time --target=...`, `restore-pitr.sh` restaura o backup mais
recente (não é PITR de verdade, é só "voltar pro último backup" -- útil
se só quiseres desfazer algo recente sem mirar um instante exato).

Depois de promover, um PITR sempre entra numa **timeline nova** (o
Postgres cria `00000002.history` etc. -- é assim que ele distingue "o
que aconteceu antes do restore" de "o que aconteceu depois", mesmo que
tenhas restaurado pro passado). Vale correr um backup full logo a
seguir (`run-backup.sh full`) pra reancorar a cadeia de backups nessa
timeline nova, em vez de depender só dos backups da timeline anterior.

### Trocar o storage de backup (posix → S3/MinIO)

Não precisas trocar de ferramenta nem de scripts -- só as variáveis de
ambiente do serviço `postgres` no `docker-compose.yml`:
```yaml
PGBACKREST_REPO_TYPE: s3
PGBACKREST_S3_BUCKET: meu-bucket
PGBACKREST_S3_ENDPOINT: minio.exemplo.local:9000
PGBACKREST_S3_REGION: us-east-1     # MinIO aceita qualquer valor aqui
PGBACKREST_S3_KEY: ...
PGBACKREST_S3_KEY_SECRET: ...
```
Depois de trocar, precisas recriar a stanza contra o novo repo (o
pgBackRest não migra backups antigos de um repo pro outro sozinho):
```sh
docker compose -f services/database/docker-compose.yml exec postgres \
  pgbackrest --stanza=shared stanza-create
docker compose -f services/database/docker-compose.yml exec postgres run-backup.sh full
```

## Limitações conhecidas

- pgBackRest corre no mesmo container que o Postgres (via cron), não num
  "repository host" separado -- é o padrão mais simples de construir
  agora; separar exigiria configurar o protocolo remoto do pgBackRest
  (SSH ou TLS) entre dois hosts, o que é o padrão mais correto de
  produção, mas fica pra uma fase futura.
- Adminer não tem autenticação própria forte (sem MFA, sem rate-limit) --
  só para dev, nunca exposto publicamente.
- Sem replicação/read-replica -- uma única instância Postgres.
- O `services/auth/docker-compose.yml` standalone continua com o seu
  próprio Postgres dedicado (fica autossuficiente pra quem só quiser o
  auth isolado) -- é no `demo/docker-compose.yml` que o auth já usa este
  serviço partilhado de verdade (ver `demo/README.md`, secção "Auth +
  portal + database", e `demo/db-init/01-auth-role.sql` como exemplo de
  provisionamento de role por serviço).
- Criação de roles/schemas por serviço (ex: um utilizador Postgres com
  acesso só ao seu próprio schema) não é automatizada por este serviço --
  cada projeto que o consome monta os seus próprios scripts em
  `/docker-entrypoint-initdb.d` (ver exemplo em `demo/db-init/`) ou faz
  manualmente via Adminer/`psql`. Isto é deliberado: o `database` não
  precisa saber nada sobre os serviços que o usam.
- Sem teste de restore automatizado -- um backup nunca testado não é um
  backup confiável; validar periodicamente com o passo a passo de PITR
  acima (contra um volume descartável) fica como responsabilidade
  operacional, não está automatizado ainda.
