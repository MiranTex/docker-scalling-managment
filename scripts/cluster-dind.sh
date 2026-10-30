#!/usr/bin/env bash
# Cluster de bolso: uma plataforma em modo cluster e dois nós, cada um
# num Docker-in-Docker, tudo no Docker desta máquina (ex: o Docker
# Desktop do Mac). Serve para desenvolver e testar o cluster sem servidores;
# a validação final é em máquinas Linux reais.
#
#   scripts/cluster-dind.sh up      monta tudo (plataforma + 2 nós ligados)
#   scripts/cluster-dind.sh test    teste de ponta a ponta (deploy nos nós)
#   scripts/cluster-dind.sh reload launcher portal ...
#                                   leva imagens reconstruídas (basestack
#                                   dev build) para a plataforma e aplica
#   scripts/cluster-dind.sh down    apaga tudo
#
# Precisa das imagens da plataforma (basestack dev build) e dos binários
# (make release). Rede 172.31.0.0/24: plataforma em .10, nós em .11 e .12.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
NET=bs-dind
CP=bs-cp
CP_IP=172.31.0.10
DOMAIN=$CP_IP.nip.io
NODES=(bs-node-1 bs-node-2)
DIND=docker:29-dind
ADMIN=admin@example.test
PASSWORD=cluster-dind-pass

case "$(docker info -f '{{.Architecture}}')" in
  aarch64 | arm64) ARCH=arm64 ;;
  *) ARCH=amd64 ;;
esac

log() { printf '\n==> %s\n' "$*"; }

wait_docker() { # espera que o Docker dentro do container responda
  for _ in $(seq 60); do
    docker exec "$1" docker info >/dev/null 2>&1 && return 0
    sleep 1
  done
  echo "o Docker de $1 não arrancou" >&2
  return 1
}

start_dind() { # nome ip
  docker run -d --privileged --name "$1" --hostname "$1" --network $NET --ip "$2" \
    -v "$1-docker:/var/lib/docker" $DIND >/dev/null
  wait_docker "$1"
  docker cp "$ROOT/bin/release/basestack-linux-$ARCH" "$1:/usr/local/bin/basestack"
}

# trust_ca põe a CA da plataforma num nó (certs.d e CAs do sistema) e
# reinicia o Docker dele -- o que o "basestack node join" pediria com sudo.
trust_ca() {
  local host=registry.$DOMAIN tmp
  tmp=$(mktemp)
  docker cp $CP:/var/lib/basestack/tls/ca.crt "$tmp"
  docker exec "$1" mkdir -p "/etc/docker/certs.d/$host"
  docker cp "$tmp" "$1:/etc/docker/certs.d/$host/ca.crt"
  docker cp "$tmp" "$1:/usr/local/share/ca-certificates/basestack-$host.crt"
  rm -f "$tmp"
  docker exec "$1" update-ca-certificates >/dev/null 2>&1
  docker restart "$1" >/dev/null
  wait_docker "$1"
}

up() {
  [ -x "$ROOT/bin/release/basestack-linux-$ARCH" ] || { echo "falta bin/release/basestack-linux-$ARCH -- make release" >&2; exit 1; }
  docker network inspect $NET >/dev/null 2>&1 || docker network create --subnet 172.31.0.0/24 $NET >/dev/null

  log "plataforma ($CP, $CP_IP)"
  start_dind $CP $CP_IP
  log "imagens da plataforma para dentro do $CP"
  local images=()
  for i in database auth secretsadmin templatesadmin launcher portal autoscaler-group agent; do
    images+=("basestack/$i:dev")
  done
  docker save --platform "linux/$ARCH" "${images[@]}" | docker exec -i $CP docker load -q
  docker exec $CP basestack init --yes --cluster --domain $DOMAIN --admin-email $ADMIN
  docker exec $CP basestack up >/dev/null
  # O Docker da plataforma só confia no seu registry depois de reiniciar
  # (sem systemd aqui); o segundo up já põe o agente no registry.
  docker restart $CP >/dev/null
  wait_docker $CP
  docker exec $CP basestack up | tail -8

  local ip=11
  for n in "${NODES[@]}"; do
    log "nó $n (172.31.0.$ip)"
    start_dind "$n" "172.31.0.$ip"
    trust_ca "$n"
    local cmd
    cmd=$(docker exec $CP basestack node token | grep -o 'basestack node join .*')
    docker exec "$n" sh -c "$cmd --name $n"
    ip=$((ip + 1))
  done
  log "pronto: portal em http://portal.$DOMAIN (só dentro da rede $NET); $0 test"
}

# client corre curl/jq num container na rede do cluster (o Mac não
# alcança a rede interna do Docker Desktop).
client() {
  docker exec bs-client "$@"
}

jqc() { # jq no cliente, a ler do stdin
  docker exec -i bs-client jq "$@"
}

api() { # método caminho [json]
  client curl -sS -b /tmp/jar -c /tmp/jar -X "$1" -H 'Content-Type: application/json' \
    ${3:+--data "$3"} "http://$CP_IP:3100/api/platform/$2"
}

test() {
  docker rm -f bs-client >/dev/null 2>&1 || true
  docker run -d --name bs-client --network $NET alpine:3.20 sleep infinity >/dev/null
  client apk add -q curl jq >/dev/null

  log "conta de admin"
  client curl -sS -o /dev/null -X POST -H 'Content-Type: application/json' \
    --data "{\"email\":\"$ADMIN\",\"password\":\"$PASSWORD\"}" "http://$CP_IP:3100/api/auth/register" || true
  docker exec $CP basestack restart auth >/dev/null 2>&1
  sleep 5
  client curl -sS -f -o /dev/null -c /tmp/jar -X POST -H 'Content-Type: application/json' \
    --data "{\"email\":\"$ADMIN\",\"password\":\"$PASSWORD\"}" "http://$CP_IP:3100/api/auth/login"

  log "nós"
  api GET nodes | jqc -r '.[] | "\(.name)\t\(.kind)\t\(.address)\t\(.os)/\(.arch)\tonline=\(.online)\trede=\(.network)"'

  log "projeto demo/prod com Postgres e um nginx de 2 réplicas, só nos nós remotos"
  api POST projects '{"slug":"demo"}' >/dev/null
  api POST projects/demo/environments '{"slug":"prod"}' >/dev/null
  api POST projects/demo/environments/prod/addons '{"slug":"db","kind":"postgres"}' >/dev/null
  api POST projects/demo/environments/prod/services '{"slug":"web","kind":"web","port":80,"replicas":2}' >/dev/null
  api PATCH nodes/local '{"schedulable":false}' >/dev/null
  local dep
  dep=$(api POST projects/demo/environments/prod/services/web/deployments '{"image":"nginx:alpine"}' | jqc -r .id)
  local status=""
  for _ in $(seq 120); do
    status=$(api GET "projects/demo/environments/prod/services/web/deployments/$dep" | jqc -r .status)
    [ "$status" = succeeded ] || [ "$status" = failed ] && break
    sleep 2
  done
  api PATCH nodes/local '{"schedulable":true}' >/dev/null
  echo "deploy: $status"
  [ "$status" = succeeded ] || { api GET "projects/demo/environments/prod/services/web/deployments/$dep" | jqc -r '.error, .log'; exit 1; }

  log "réplicas (nó de cada uma)"
  api GET projects/demo/environments/prod/services/web/replicas | jqc -r '.[] | "\(.name)\t\(.node)\tpronta=\(.ready)"'

  log "Traefik (na plataforma) -> réplica num nó remoto, pela overlay"
  client curl -sS -H "Host: web-prod-demo.$DOMAIN" "http://$CP_IP/" | grep -o '<title>.*</title>'

  log "réplica num nó remoto -> Postgres na plataforma, pela overlay"
  local node replica
  node=$(api GET projects/demo/environments/prod/services/web/replicas | jqc -r '.[0].node')
  replica=$(api GET projects/demo/environments/prod/services/web/replicas | jqc -r '.[0].name')
  docker exec "$node" docker exec "$replica" sh -c \
    'h=$(echo "$DATABASE_URL" | sed -E "s#.*@([^:/]+).*#\1#"); nc -z -w 3 "$h" 5432 && echo "Postgres ($h) alcançável a partir de $(hostname)"'
  log "tudo certo"
}

reload() {
  local images=()
  for i in "$@"; do images+=("basestack/$i:dev"); done
  docker save --platform "linux/$ARCH" "${images[@]}" | docker exec -i $CP docker load -q
  docker exec $CP basestack up | tail -3
}

down() {
  docker rm -f bs-client $CP "${NODES[@]}" >/dev/null 2>&1 || true
  docker volume rm $CP-docker "${NODES[@]/%/-docker}" >/dev/null 2>&1 || true
  docker network rm $NET >/dev/null 2>&1 || true
  echo "cluster de bolso apagado"
}

case "${1:-}" in
  up) up ;;
  test) test ;;
  reload) shift; reload "$@" ;;
  down) down ;;
  *) echo "uso: $0 up|test|reload <imagem>...|down" >&2; exit 2 ;;
esac
