# demo

Demo end-to-end ligando [services/autoscaler](../services/autoscaler) e
[services/monitoring](../services/monitoring): um serviço "demo" (nginx)
escalando de verdade, com métricas e logs visíveis no Grafana.

## Passo a passo

1. Suba a plataforma de observabilidade (uma vez só):
   ```sh
   docker compose -f services/monitoring/docker-compose.yml up -d
   ```
2. Construa a imagem do autoscaler (repita sempre que mudar o código):
   ```sh
   docker build -t autoscaler-group:latest services/autoscaler
   ```
3. Suba o demo:
   ```sh
   docker compose -f demo/docker-compose.yml up -d
   ```

O `group-demo` já nasce configurado com `LAUNCH_TEMPLATE_FILE` apontando
pra [launch-template.json](launch-template.json) (deste diretório) — cria
sozinho a(s) réplica(s) do serviço "demo" (nginx:alpine), inclusive a
primeira, sem precisar de bootstrap manual.

## Onde olhar

- **Proxy do serviço demo**: `http://localhost:8095`
- **Health/métricas do group**: `http://localhost:9095/healthz`,
  `/readyz`, `/metrics`
- **Grafana**: `http://localhost:3000` (dashboard "Autoscaler" já
  provisionado — métricas de réplicas, CPU, scale up/down, e logs)
- **Prometheus**: `http://localhost:9091` (confira em Status → Targets que
  `group-demo` está `UP`)

## Portas 8095/9095 (não 8090/9090)

Ajustadas pra não colidir com outro projeto rodando no mesmo host — se não
for o teu caso, pode simplificar de volta pra 8090/9090 em
`demo/docker-compose.yml`.

## Derrubar

```sh
docker compose -f demo/docker-compose.yml down
```

O group remove as réplicas que criou como parte do seu shutdown gracioso —
não fica nada órfão. A plataforma de observabilidade (`services/monitoring`)
não é afetada; derrube-a separadamente se quiser:
```sh
docker compose -f services/monitoring/docker-compose.yml down
```
