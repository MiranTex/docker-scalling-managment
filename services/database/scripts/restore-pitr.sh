#!/usr/bin/env bash
# Helper de restore -- NÃO corre automaticamente em lado nenhum. É uma
# operação manual e destrutiva (sobrescreve o diretório de dados), por
# isso não faz sentido automatizá-la atrás de um cron ou correr dentro do
# mesmo processo que já está a servir tráfego.
#
# Pré-requisito: o Postgres deste container tem de estar PARADO antes de
# restaurar -- não dá para pgbackrest reescrever o PGDATA com o Postgres
# vivo em cima dele. Por isso este script assume que foi invocado com o
# ENTRYPOINT da imagem trocado (ver README "Backups e PITR -- como
# restaurar"), não durante o arranque normal do container:
#
#   docker compose -f services/database/docker-compose.yml stop postgres
#   docker compose -f services/database/docker-compose.yml run --rm \
#     --entrypoint restore-pitr.sh postgres --type=time --target="2026-08-04 10:00:00"
#   docker compose -f services/database/docker-compose.yml up -d postgres
#
# Sem argumentos, restaura o backup mais recente (não é PITR, é "voltar
# pro último backup"). Com --type=time --target=..., é PITR de verdade:
# reaplica o WAL arquivado até o instante pedido.
set -euo pipefail

# Mesma razão dos outros scripts: pgBackRest recusa rodar como root, e
# `--entrypoint restore-pitr.sh` entra neste script direto como root
# (não passa pelo docker-entrypoint.sh original, que é quem normalmente
# baixaria privilégio).
pgbackrest() {
  if [[ "$(id -u)" == "0" ]]; then
    gosu postgres pgbackrest "$@"
  else
    command pgbackrest "$@"
  fi
}

: "${PGBACKREST_STANZA:=shared}"

# Este container foi iniciado com --entrypoint restore-pitr.sh, então o
# entrypoint-wrapper.sh normal (que gera o pgbackrest.conf) nunca correu
# -- temos de gerar a config nós mesmos antes de chamar `pgbackrest
# restore`.
generate-pgbackrest-conf.sh

echo "[restore-pitr] AVISO: isto vai sobrescrever /var/lib/postgresql/data."
echo "[restore-pitr] stanza=${PGBACKREST_STANZA} args: $*"
read -r -p "[restore-pitr] confirmas? (escreve 'sim' para continuar) " confirm
if [[ "$confirm" != "sim" ]]; then
  echo "[restore-pitr] cancelado."
  exit 1
fi

# --delta permite restaurar por cima de um PGDATA que já tem ficheiros
# (reaproveita o que já bate com o backup, só copia o que mudou) --
# funciona tanto num PGDATA vazio quanto num já existente.
pgbackrest --stanza="$PGBACKREST_STANZA" --delta restore "$@"

echo "[restore-pitr] restore concluído. Sobe o serviço normalmente:"
echo "  docker compose -f services/database/docker-compose.yml up -d postgres"
echo "[restore-pitr] o Postgres vai continuar em modo de recovery até"
echo "aplicar todo o WAL necessário -- acompanha com 'docker compose logs -f postgres'."
