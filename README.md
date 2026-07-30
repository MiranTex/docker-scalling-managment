# base-stack

Este repositório é a "base stack": um conjunto de serviços independentes, cada
um no seu próprio diretório, que juntos dão a um projeto Docker autoscaling +
observabilidade sem precisar reinventar isso a cada novo projeto. A ideia é
ir adicionando serviços novos aqui conforme surgir necessidade, cada um
isolado dos demais e documentado no seu próprio README.

## Estrutura

```
services/
  autoscaler/    autoscaler + load balancer de containers Docker (Go)
  monitoring/    plataforma de observabilidade: Prometheus + Loki + Promtail + Grafana
  auth/          autenticação: registo/login, JWT (RS256) + JWKS, refresh tokens
demo/            compose de exemplo ligando autoscaler + monitoring, pra testar end-to-end
```

Cada serviço em `services/` é autossuficiente: tem seu próprio
`docker-compose.yml` (ou instruções de build) e README, e pode ser usado
isoladamente em qualquer projeto, sem depender dos outros. `demo/` existe só
pra mostrar os dois funcionando juntos e servir de referência de integração.

## Serviços

- **[services/autoscaler](services/autoscaler/README.md)** — escala
  containers de um serviço-alvo pra cima/baixo com base em CPU, faz load
  balancing e health check entre as réplicas, drena conexões antes de
  remover um container, expõe métricas Prometheus e logs estruturados.
- **[services/monitoring](services/monitoring/README.md)** — Prometheus,
  Loki, Promtail e Grafana, pensados pra subir uma vez e servir múltiplos
  projetos ao mesmo tempo (auto-descoberta via labels/socket Docker, sem
  precisar editar config a cada projeto novo).
- **[services/auth](services/auth/README.md)** — registo/login por
  email+senha, JWT RS256 com JWKS pra validação sem segredo partilhado, e
  refresh tokens com rotação e deteção de reuso. Fase 1 de um serviço
  pensado pra crescer com API keys, OAuth2/OIDC e WebAuthn/passkeys.

## Demo end-to-end

Ver **[demo/README.md](demo/README.md)** pra subir tudo junto e ver
métricas/logs de um serviço de exemplo escalando ao vivo no Grafana.

## Adicionando um novo serviço

Cria um diretório em `services/<nome>/` com o que for necessário (compose,
Dockerfile, configs) e um `README.md` explicando o que é e como usar. Se o
serviço precisar se integrar com os demais (ex: expor métricas pro
monitoring), documenta isso no README dele — o padrão usado pelo autoscaler
(rede externa `observability-net` + labels `prometheus.scrape`/
`prometheus.port`) é um bom exemplo a seguir.
