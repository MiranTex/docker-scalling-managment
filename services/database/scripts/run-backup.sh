#!/usr/bin/env bash
# Wrapper fino chamado pelo cron (ver crontab gerado em
# entrypoint-wrapper.sh) ou manualmente por ti:
#   docker compose exec postgres run-backup.sh full   # ou: diff | incr
#
# Só existe para dar um nome claro ao comando no crontab (em vez de
# repetir a flag --stanza em todo lado) e para falhar alto (`set -e`) se
# o tipo de backup vier errado -- um cron job que falha silenciosamente
# por causa de typo no crontab é o pior tipo de bug de infra: você só
# descobre quando precisa restaurar e não há backup nenhum.
set -euo pipefail

# Mesma razão do ensure-stanza.sh: pgBackRest recusa rodar como root. O
# cron chama este script já como user `postgres` (ver o crontab gerado
# em entrypoint-wrapper.sh), mas se chamares isto manualmente via
# `docker compose exec postgres run-backup.sh ...` sem `-u postgres`,
# cais aqui como root -- então baixamos privilégio nós mesmos em vez de
# depender de quem chama lembrar da flag.
pgbackrest() {
  if [[ "$(id -u)" == "0" ]]; then
    gosu postgres pgbackrest "$@"
  else
    command pgbackrest "$@"
  fi
}

: "${PGBACKREST_STANZA:=shared}"
backup_type="${1:?uso: run-backup.sh full|diff|incr}"

case "$backup_type" in
  full|diff|incr) ;;
  *)
    echo "[run-backup] tipo inválido: '$backup_type' (use full, diff ou incr)" >&2
    exit 1
    ;;
esac

echo "[run-backup] $(date -Is) iniciando backup --type=${backup_type} (stanza=${PGBACKREST_STANZA})"
pgbackrest --stanza="$PGBACKREST_STANZA" --type="$backup_type" backup
echo "[run-backup] $(date -Is) backup concluído"
