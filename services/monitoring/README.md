# monitoring

Plataforma de observabilidade — Prometheus + Loki + Promtail + Grafana —
pensada pra subir **uma vez** e servir **múltiplos projetos** ao mesmo
tempo, sem precisar editar configuração central a cada novo serviço.

## Componentes

- **Prometheus** — coleta métricas. Descobre containers sozinho via
  `docker_sd_configs` (lendo o socket Docker), filtrando por labels — não
  precisa listar serviços manualmente em `prometheus.yml`.
- **Loki + Promtail** — agregação de logs. Promtail lê os logs de
  `/var/lib/docker/containers` de **qualquer** container do host (não
  precisa estar na mesma rede), envia pro Loki.
- **Grafana** — dashboards. Provisionado com datasources (Prometheus + Loki)
  e um dashboard de exemplo já prontos (`grafana/provisioning`,
  `grafana/dashboards`).

## Subir

```sh
docker compose -f services/monitoring/docker-compose.yml up -d
```

Cria a rede externa `observability-net`, que qualquer outro compose pode se
conectar (`networks: { observability-net: { external: true } }`).

Portas no host: Prometheus `9091`, Loki `3100`, Grafana `3000`
(login anônimo habilitado, sem senha).

## Como um projeto novo aparece aqui

Pra métricas (Prometheus + Grafana):
1. Conectar o container à rede `observability-net`.
2. Expor um endpoint `/metrics` em formato Prometheus.
3. Ter as labels `prometheus.scrape: "true"` e `prometheus.port: "<porta>"`.

Pra logs (Loki + Grafana): nada a fazer — Promtail já lê logs de todo
container do host via socket Docker, independente de rede.

Em ambos os casos, use uma label própria (ex: `service: "<nome>"`) nos
containers do seu projeto se quiser filtrar por serviço no Grafana — evite
reusar labels que o próprio Prometheus usa internamente (ex: `service` sem
prefixo colide com o `compose_service` que o `docker_sd_configs` já gera).

## Recursos (mem/cpu)

Limites definidos no compose pra não deixar a stack de observabilidade
competir demais com o(s) projeto(s) monitorado(s):
Prometheus 256MB/0.5 CPU, Loki 256MB/0.5 CPU, Promtail 128MB/0.3 CPU,
Grafana 384MB/0.5 CPU (ajustado depois de medir ~154MB de uso real —
256MB batia quase 100% no boot por causa do carregamento de plugins).

## Nota específica do host

O container do Prometheus roda como usuário não-root e precisa do GID do
grupo `docker` do host pra ler `/var/run/docker.sock` (ver `group_add: ["999"]`
em `docker-compose.yml`). Se `999` não bater no seu host, confirme com:

```sh
getent group docker
```
