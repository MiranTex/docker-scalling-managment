#!/usr/bin/env bash
# ENTRYPOINT desta imagem. Corre como PID 1 do container, mas só até ao
# fim deste script: a partir do `exec dbadmin` no fim, é o dbadmin quem
# passa a ser o PID 1 (ver services/database/dbadmin) -- ele é quem
# efetivamente arranca o Postgres (como seu processo FILHO, via
# docker-entrypoint.sh) e o entrega o controlo. Isto existe por causa do
# PITR: dbadmin precisa conseguir parar/arrancar o Postgres outra vez sem
# derrubar o container inteiro, o que só é possível se o Postgres NÃO for
# o PID 1.
#
# Este script faz 4 coisas ANTES de entregar o controlo ao dbadmin:
#   1. gera /etc/pgbackrest/pgbackrest.conf a partir do template +
#      variáveis de ambiente (é aqui que "storage do backup trocável"
#      vira realidade -- ver bloco REPO1 abaixo)
#   2. garante que o diretório do repo local existe e tem dono certo,
#      se o repo for do tipo posix
#   3. gera o crontab de backups a partir de BACKUP_SCHEDULE_FULL/INCR e
#      arranca o cron em background
#   4. dispara ensure-stanza.sh em background (não bloqueia o boot --
#      ele mesmo espera o Postgres responder antes de agir)
set -euo pipefail

: "${PGBACKREST_STANZA:=shared}"
: "${BACKUP_SCHEDULE_FULL:=0 2 * * 0}"   # domingo 02:00
: "${BACKUP_SCHEDULE_INCR:=0 2 * * 1-6}" # seg-sáb 02:00

export PGBACKREST_STANZA

# A geração do pgbackrest.conf vive em generate-pgbackrest-conf.sh, não
# aqui -- restore-pitr.sh também precisa dela (quando corre via
# `--entrypoint restore-pitr.sh`, este script nunca é executado).
generate-pgbackrest-conf.sh

# --- Agendamento dos backups via cron -------------------------------
# Cron dentro do mesmo container que o Postgres não é o ideal "um
# processo por container", mas é a opção mais simples para já (ver
# README "Limitações conhecidas" -- separar isto num repository host
# próprio é o padrão de produção do pgBackRest, fica para uma fase
# futura). O output do cron vai para o stdout do container (fd 1 do
# PID 1) para aparecer em `docker logs` junto com o Postgres.
echo "[entrypoint-wrapper] agendando backups: full='${BACKUP_SCHEDULE_FULL}' incr='${BACKUP_SCHEDULE_INCR}'"
cat > /etc/cron.d/pgbackrest <<EOF
PGBACKREST_STANZA=${PGBACKREST_STANZA}
${BACKUP_SCHEDULE_FULL} postgres run-backup.sh full >> /proc/1/fd/1 2>> /proc/1/fd/2
${BACKUP_SCHEDULE_INCR} postgres run-backup.sh incr >> /proc/1/fd/1 2>> /proc/1/fd/2
EOF
chmod 0644 /etc/cron.d/pgbackrest
cron

# ensure-stanza.sh corre em paralelo ao arranque do Postgres, não antes
# dele -- só entra em ação quando pg_isready responder (ver o próprio
# script). Rodar em background aqui evita atrasar o boot do container.
PGBACKREST_STANZA="$PGBACKREST_STANZA" ensure-stanza.sh &

# --- dbadmin: PID 1 a partir daqui -----------------------------------
# dbadmin é quem arranca o Postgres de verdade (como seu processo
# filho, com os -c que ligam o WAL archiving pro pgBackRest -- ver
# cmd/dbadmin/main.go, postgresArgs) e expõe a API interna de
# administração (estado, backups, PITR) usada pelo portal. "$@" vem do
# CMD da imagem (["postgres"], ver Dockerfile) -- dbadmin repassa isto a
# docker-entrypoint.sh, exatamente como este script fazia antes
# diretamente.
: "${DBADMIN_LISTEN_ADDR:=:8091}"
DBADMIN_DATABASE_URL="postgres://${POSTGRES_USER:-app}:${POSTGRES_PASSWORD}@localhost:5432/${POSTGRES_DB:-app}?sslmode=disable"
echo "[entrypoint-wrapper] entregando controlo ao dbadmin (${DBADMIN_LISTEN_ADDR}), que arranca o Postgres"
export DBADMIN_LISTEN_ADDR
export DBADMIN_DATABASE_URL
exec dbadmin "$@"
