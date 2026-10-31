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
    # Mesmo bloco serve para qualquer storage S3-compatible -- só o
    # endpoint muda. Ver README "Trocar o storage de backup" para as
    # variáveis exigidas aqui. Chamam-se BACKUP_S3_* e não PGBACKREST_*:
    # o pgBackRest lê toda variável PGBACKREST_X como a opção X e avisa
    # em cada comando das que não conhece.
    : "${BACKUP_S3_VERIFY_TLS:=y}"
    # host = o bucket é um subdomínio do endpoint (AWS); path = vai no
    # caminho -- o storage interno da basestack só tem um nome ("storage").
    : "${BACKUP_S3_URI_STYLE:=host}"
    : "${BACKUP_S3_PORT:=443}"
    cat > /etc/pgbackrest/pgbackrest.conf <<EOF
[global]
repo1-retention-full=${PGBACKREST_RETENTION_FULL}
process-max=${PGBACKREST_PROCESS_MAX}
log-level-console=info
log-level-file=detail
repo1-type=s3
repo1-s3-bucket=${BACKUP_S3_BUCKET:?BACKUP_S3_BUCKET obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-endpoint=${BACKUP_S3_ENDPOINT:?BACKUP_S3_ENDPOINT obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-region=${BACKUP_S3_REGION:?BACKUP_S3_REGION obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-key=${BACKUP_S3_KEY:?BACKUP_S3_KEY obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-s3-key-secret=${BACKUP_S3_KEY_SECRET:?BACKUP_S3_KEY_SECRET obrigatório quando PGBACKREST_REPO_TYPE=s3}
repo1-storage-verify-tls=${BACKUP_S3_VERIFY_TLS}
repo1-s3-uri-style=${BACKUP_S3_URI_STYLE}
repo1-storage-port=${BACKUP_S3_PORT}
EOF
    # Uma CA própria (ex: a da instalação basestack) em vez das do sistema.
    if [[ -n "${BACKUP_S3_CA_FILE:-}" ]]; then
      echo "repo1-storage-ca-file=${BACKUP_S3_CA_FILE}" >> /etc/pgbackrest/pgbackrest.conf
    fi
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
