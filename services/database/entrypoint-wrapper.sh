#!/usr/bin/env bash
# ENTRYPOINT desta imagem -- substitui o entrypoint.sh oficial do Postgres
# (que continuamos a chamar no fim, via `exec`, pra não perder nenhum
# comportamento de arranque do image original: criação do PGDATA na
# primeira vez, /docker-entrypoint-initdb.d, etc).
#
# Corre como PID 1 do container. Faz 4 coisas ANTES de entregar o
# controlo ao Postgres:
#   1. gera /etc/pgbackrest/pgbackrest.conf a partir do template +
#      variáveis de ambiente (é aqui que "storage do backup trocável"
#      vira realidade -- ver bloco REPO1 abaixo)
#   2. garante que o diretório do repo local existe e tem dono certo,
#      se o repo for do tipo posix
#   3. gera o crontab de backups a partir de BACKUP_SCHEDULE_FULL/INCR e
#      arranca o cron em background
#   4. dispara ensure-stanza.sh em background (não bloqueia o boot --
#      ele mesmo espera o Postgres responder antes de agir)
#
# Só DEPOIS disso é que fazemos exec do entrypoint real do Postgres, já
# com os -c extra que ligam o WAL archiving pro pgBackRest.
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

# A partir daqui, quem manda é o entrypoint oficial do Postgres. Os -c
# extra ligam o WAL continuamente para o pgBackRest (arquivamento
# contínuo é o que torna PITR possível -- sem isto, pgBackRest só teria
# os backups full/incr, sem conseguir restaurar para um instante entre
# dois backups).
# "$@" vem do CMD da imagem (["postgres"], ver Dockerfile) -- vai
# PRIMEIRO, e os -c extra depois, porque o binário postgres só aceita
# -c/outras opções depois do comando "postgres" em si; repetir "postgres"
# de novo no fim (como uma versão anterior deste script fazia) faz o
# binário tratá-lo como um argumento posicional inválido.
exec docker-entrypoint.sh "$@" \
  -c archive_mode=on \
  -c "archive_command=pgbackrest --stanza=${PGBACKREST_STANZA} archive-push %p" \
  -c wal_level=replica \
  -c max_wal_senders=3 \
  -c archive_timeout=60
