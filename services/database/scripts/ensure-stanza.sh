#!/usr/bin/env bash
# Roda em background, disparado pelo entrypoint-wrapper.sh, em paralelo
# ao arranque do Postgres (não bloqueia o boot do container).
#
# `pgbackrest stanza-create` só pode ser chamado com o Postgres já a
# aceitar ligações (ele lê o pg_control real para validar a stanza) --
# por isso este script espera pg_isready antes de fazer nada.
#
# Chamamos `stanza-create` sempre, sem checar existência antes: é seguro
# (a própria ferramenta não faz nada de destrutivo se a stanza já existir
# e continuar a bater com o PGDATA atual -- só falha se algo mudou de
# forma incompatível, o que exigiria intervenção manual mesmo). Uma
# tentativa anterior tentava detetar "já existe" fazendo grep no JSON de
# `pgbackrest info`, mas esse JSON sempre inclui `"name":"<stanza>"`
# mesmo quando ela NÃO existe (é só o nome que perguntaste, não uma
# confirmação) -- dava sempre falso positivo e nunca criava a stanza de
# verdade.
set -euo pipefail

: "${PGBACKREST_STANZA:=shared}"

# pgBackRest recusa-se a rodar como root (é uma proteção da própria
# ferramenta -- evita criar ficheiros no repo que o user postgres depois
# não consiga ler). Este script é lançado em background pelo
# entrypoint-wrapper.sh, que ainda está como root nesse ponto do boot,
# então precisamos baixar privilégio explicitamente. `gosu` já vem na
# imagem oficial do Postgres (é o que o docker-entrypoint.sh usa
# internamente pra a mesma coisa).
pgbackrest() {
  if [[ "$(id -u)" == "0" ]]; then
    gosu postgres pgbackrest "$@"
  else
    command pgbackrest "$@"
  fi
}

echo "[ensure-stanza] à espera do Postgres ficar pronto..."
until pg_isready -h /var/run/postgresql -U postgres >/dev/null 2>&1; do
  sleep 1
done

# Na primeira inicialização de um PGDATA vazio, a imagem oficial do
# Postgres sobe uma instância TEMPORÁRIA só pra rodar o initdb/scripts de
# /docker-entrypoint-initdb.d, depois derruba-a e sobe a instância real --
# e o `pg_isready` acima pode responder "accepting connections" contra
# essa instância temporária, um instante antes dela desligar. Se
# `stanza-create` calhar bem nesse intervalo, falha com "unable to find
# primary cluster" mesmo o Postgres estando saudável segundos depois.
# Por isso repetimos com backoff em vez de deixar `set -e` abortar na
# primeira tentativa.
echo "[ensure-stanza] garantindo stanza '${PGBACKREST_STANZA}' (idempotente)..."
attempt=1
max_attempts=10
until pgbackrest --stanza="$PGBACKREST_STANZA" stanza-create; do
  if (( attempt >= max_attempts )); then
    echo "[ensure-stanza] stanza-create falhou depois de ${max_attempts} tentativas, desistindo." >&2
    exit 1
  fi
  echo "[ensure-stanza] stanza-create falhou (tentativa ${attempt}/${max_attempts}), a repetir em 3s..."
  sleep 3
  ((attempt++))
done

echo "[ensure-stanza] validando (pgbackrest check)..."
pgbackrest --stanza="$PGBACKREST_STANZA" check

echo "[ensure-stanza] pronto. Considera rodar um backup full manual agora:"
echo "  docker compose exec postgres run-backup.sh full"
