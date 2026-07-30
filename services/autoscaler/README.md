# autoscaler

Um "autoscaling group" para containers Docker, no espírito de um Auto
Scaling Group da AWS: um único processo (`cmd/group`) monitora um serviço,
decide quando escalar pra cima ou pra baixo com base em CPU, cria/remove
réplicas a partir de um *launch template*, e faz load balancing +
health check entre elas. Fala diretamente com a API HTTP do Docker Engine
via unix socket — sem depender do SDK oficial.

## O que uma instância do group faz

- **Escala** o serviço-alvo pra cima/baixo olhando CPU média das réplicas,
  com anti-flapping (histerese entre limiar de subida/descida, N ticks
  consecutivos antes de agir, cooldown entre ações).
- **Nunca fica abaixo do mínimo**: se o número de réplicas cai abaixo de
  `MIN_REPLICAS` (inclusive no bootstrap, partindo de zero), escala na hora,
  sem esperar os ticks/cooldown de uma decisão normal.
- **Cria réplicas a partir de um launch template** (JSON), nunca clonando um
  container já existente — é isso que permite escalar do zero.
- **Faz load balancing round-robin** entre as réplicas saudáveis (health
  check TCP ou HTTP, configurável).
- **Drena antes de remover**: ao escolher um container pra scale down, tira
  ele do load balancer primeiro, espera `DRAIN_TIMEOUT_SECONDS` pras
  requisições em andamento terminarem, só então para e remove.
- **Desliga graciosamente**: ao receber SIGTERM/SIGINT, drena as requisições
  HTTP em andamento (`SHUTDOWN_TIMEOUT_SECONDS`) e depois **remove todas as
  réplicas que gerencia** — igual apagar um Auto Scaling Group na AWS
  termina as instâncias. Isso evita réplicas órfãs quando o group é
  desligado via `docker compose down`. Efeito colateral: um crash/restart do
  próprio group também limpa e reconstrói as réplicas do zero.
- **Expõe observabilidade**: `/healthz`, `/readyz` e `/metrics` (Prometheus)
  num servidor HTTP separado do proxy, logs estruturados via `log/slog`
  (JSON por padrão).

## Launch template

Descreve como criar uma réplica nova — imagem, comando, env, labels,
volumes, rede, `/etc/hosts` extra. Ver [launch-template.json.example](launch-template.json.example)
pros campos disponíveis e [launch-template.laravel-web.example.json](launch-template.laravel-web.example.json)
pra um exemplo real (serviço Laravel Sail).

Pontos importantes:
- **Sem `${VAR}`**: valores precisam já estar resolvidos no JSON (sem
  interpolação de variáveis de ambiente como no compose).
- **Caminhos de bind absolutos**: a API do Docker não aceita `.` relativo
  como o compose aceita.
- **Sem campo `ports`**: publicar porta no host por réplica geraria conflito
  entre elas. Quem expõe o serviço ao mundo é sempre o proxy do group —
  nunca acesse uma réplica direto por porta publicada.
- **Mudar o template só afeta réplicas futuras** — não atualiza as que já
  estão rodando (rolling update/redeploy de réplicas existentes ainda não
  está implementado).

## Configuração (variáveis de ambiente)

| Variável | Default | Descrição |
|---|---|---|
| `TARGET_SERVICE` | *(obrigatória)* | Nome do serviço que esta instância gerencia (valor da label `SERVICE_LABEL`). |
| `LAUNCH_TEMPLATE_FILE` | *(obrigatória)* | Caminho, dentro do container, do JSON do launch template. |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Socket do Docker Engine. |
| `SERVICE_LABEL` | `autoscaler.service` | Label usada pra identificar containers do serviço. |
| `BACKEND_PORT` | `80` | Porta em que as réplicas escutam. |
| `LISTEN_ADDR` | `:8090` | Endereço do proxy/load balancer. |
| `METRICS_ADDR` | `:9090` | Endereço do servidor de `/healthz`, `/readyz`, `/metrics`. |
| `MIN_REPLICAS` / `MAX_REPLICAS` | `1` / `3` | Limites de réplicas. |
| `CPU_SCALE_UP_PERCENT` / `CPU_SCALE_DOWN_PERCENT` | `50` / `20` | Limiares de CPU média. |
| `SUSTAINED_TICKS` | `2` | Ticks consecutivos fora do limiar antes de agir. |
| `COOLDOWN_SECONDS` | `30` | Tempo mínimo entre duas ações de scaling. |
| `RECONCILE_TICK_SECONDS` | `3` | Intervalo entre avaliações. |
| `HEALTH_CHECK_PATH` | *(vazio = TCP)* | Rota HTTP pro health check; vazio faz só dial TCP. |
| `HEALTH_CHECK_TIMEOUT_MS` | `500` | Timeout do health check. |
| `DRAIN_TIMEOUT_SECONDS` | `10` | Tempo fora do load balancer antes de parar um container em scale down. |
| `SHUTDOWN_TIMEOUT_SECONDS` | `15` | Tempo de espera por requisições em voo ao encerrar o processo. |
| `LOG_FORMAT` | `json` | `json` ou `text`. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` ou `error`. |

## Build

```sh
cd services/autoscaler
docker build -t autoscaler-group:latest .
```

Imagem final não leva compilador nem código-fonte — só o binário
(`golang:1.25-alpine` de build, `alpine:3.20` de runtime).

## Usando em outro projeto

Este diretório não precisa ser copiado pra outro projeto: a imagem
`autoscaler-group:latest` é referenciada pelo nome (ou publique num
registry privado). Ver [docker-compose.example.yml](docker-compose.example.yml)
pra um exemplo completo de como plugar o group na frente de um serviço já
existente (nginx + group + launch template).

## Desenvolvimento local

```sh
cd services/autoscaler
cp .env.example .env  # se não existir, defina ao menos DOCKER_SOCKET
go run ./cmd/group
go test ./...
```

`cmd/server` e `cmd/loadbalancer` são binários de estudo/referência
separados (scaler e load balancer isolados, sem o launch template) — não
usados em produção, mantidos só como material de apoio.
