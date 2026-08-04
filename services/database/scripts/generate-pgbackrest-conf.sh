#!/usr/bin/env bash
# Gera /etc/pgbackrest/pgbackrest.conf a partir do template + env vars.
# Extraído do entrypoint-wrapper.sh pra um script à parte porque
# restore-pitr.sh também precisa disto: quando o restore é invocado via
# `docker compose run --rm --entrypoint restore-pitr.sh postgres ...`, o
# entrypoint-wrapper.sh normal NUNCA corre (foi ele mesmo substituído),
# então o pgbackrest.conf tem de ser gerado por quem quer que esteja no
# comando, não só no arranque normal do container.
set -euo pipefail

: "${PGBACKREST_STANZA:=shared}"
: "${PGBACKREST_REPO_TYPE:=posix}"
: "${PGBACKREST_REPO_PATH:=/var/lib/pgbackrest}"
: "${PGBACKREST_RETENTION_FULL:=2}"
: "${PGBACKREST_PROCESS_MAX:=1}"
# Mesmo default que a imagem oficial do Postgres usa quando POSTGRES_USER
# não é definido -- precisamos do mesmo valor aqui pra pg1-user (ver
# pgbackrest.conf.template) bater com a role que existe de verdade.
: "${POSTGRES_USER:=postgres}"

echo "[generate-pgbackrest-conf] repo1-type=${PGBACKREST_REPO_TYPE}"

# [global] é montado inteiro aqui em bash (não em envsubst) porque as
# chaves de repo1-* mudam de forma inteira entre posix e s3 -- é mais
# simples que tentar meter as duas variantes condicionais dentro de um
# único template. O `>` (não `>>`) recria o ficheiro do zero, então nunca
# acumula [global] duplicado de uma chamada anterior.
case "$PGBACKREST_REPO_TYPE" in
  posix)
    mkdir -p "$PGBACKREST_REPO_PATH"
    chown postgres:postgres "$PGBACKREST_REPO_PATH"
    cat > /etc/pgbackrest/pgbackrest.conf <<EOF
[global]
repo1-retention-full=${PGBACKREST_RETENTION_FULL}
process-max=${PGBACKREST_PROCESS_MAX}
log-level-console=info
log-level-file=detail
repo1-type=posix
repo1-path=${PGBACKREST_REPO_PATH}
EOF
    ;;
  s3)
    # Mesmo bloco serve para qualquer storage S3-compatible, incluindo
    # MinIO -- só o endpoint muda. Ver README "Trocar o storage de
    # backup" para as variáveis exigidas aqui.
    : "${PGBACKREST_S3_VERIFY_TLS:=y}"
    cat > /etc/pgbackrest/pgbackrest.conf <<EOF
[global]
repo1-retention-full=${PGBACKREST_RETENTION_FULL}
process-max=${PGBACKREST_PROCESS_MAX}
log-level-console=info
log-level-file=detail
repo1-type=s3
repo1-s3-bucket=${PGBACKREST_S3_BUCKET:?PGBACKREST_S3_BUCKET obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-endpoint=${PGBACKREST_S3_ENDPOINT:?PGBACKREST_S3_ENDPOINT obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-region=${PGBACKREST_S3_REGION:?PGBACKREST_S3_REGION obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-key=${PGBACKREST_S3_KEY:?PGBACKREST_S3_KEY obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-key-secret=${PGBACKREST_S3_KEY_SECRET:?PGBACKREST_S3_KEY_SECRET obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-storage-verify-tls=${PGBACKREST_S3_VERIFY_TLS}
EOF
    ;;
  *)
    echo "[generate-pgbackrest-conf] PGBACKREST_REPO_TYPE inválido: '$PGBACKREST_REPO_TYPE' (use 'posix' ou 's3')" >&2
    exit 1
    ;;
esac

# A secção da stanza (pg1-path/port/user) é igual nos dois modos -- essa
# parte vem do template, resolvida via envsubst, e é concatenada ao
# [global] que acabámos de escrever.
envsubst '${PGBACKREST_STANZA} ${POSTGRES_USER}' \
  < /etc/pgbackrest/pgbackrest.conf.template \
  >> /etc/pgbackrest/pgbackrest.conf
