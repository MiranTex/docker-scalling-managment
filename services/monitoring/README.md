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

Portas no host, **só em `127.0.0.1`** (nenhuma destas tem autenticação
própria, ou guarda dados de todos os projetos): Prometheus `9091`, Loki
`3101` (não `3100` -- colide com o `portal` do `demo/docker-compose.yml`),
Grafana `3000`. Num servidor, acede-se por túnel SSH
(`ssh -L 3000:127.0.0.1:3000 servidor`).

**Grafana com o login da plataforma.** Entra-se pelo portal, em
`/grafana` ("Métricas e logs" no menu), sem segunda password. O portal diz
ao Grafana quem é o utilizador (modo *auth proxy*), e o Grafana só aceita
essa identidade vinda da rede `grafana-proxy` (portal e launcher; o gateway
fica de fora, por isso nem os processos desta máquina a conseguem forjar).

Cada projeto é uma **organização** no Grafana, só com os dados desse
projeto, e a organização **Plataforma** tem tudo (só para admins da
plataforma). Quem é membro de um projeto entra na organização dele como
Viewer (viewer) ou Editor (developer/admin). O launcher mantém isto
sincronizado (ver `services/launcher/internal/grafanasync`). O isolamento é
feito nos dados, não só na interface:

- **métricas:** as organizações de projeto leem o Prometheus pelo
  `prom-label-proxy`, que injeta `project="<slug>"` em todas as consultas;
- **logs:** o Loki é multi-tenant e o Promtail põe os logs de cada projeto
  no tenant desse projeto (o resto vai para `platform`).

As fontes de dados e os dashboards de cada organização são criados pelo
launcher (já não há provisioning); os JSON vêm de `grafana/dashboards`.

**Acesso de emergência:** `http://localhost:3000/grafana/` com
`GRAFANA_ADMIN_USER` / `GRAFANA_ADMIN_PASSWORD` (por defeito `admin`/`admin`,
só para dev). O launcher usa estas mesmas credenciais para sincronizar.

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
