# demo

Demo end-to-end do base-stack: [services/autoscaler](../services/autoscaler)
+ [services/monitoring](../services/monitoring) (um serviço "demo" escalando
de verdade, com métricas e logs no Grafana) e, à parte,
[services/auth](../services/auth) + [services/portal](../services/portal) —
com o próprio `auth` também gerido pelo autoscaler (`group-authd`), não como
um container estático. As duas partes são independentes uma da outra — pode
subir só a que precisa.

## Autoscaler + monitoring (nginx de exemplo)

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
   docker compose -f demo/docker-compose.yml up -d group-demo
   ```

O `group-demo` já nasce configurado com `LAUNCH_TEMPLATE_FILE` apontando
pra [launch-template.json](launch-template.json) (deste diretório) — cria
sozinho a(s) réplica(s) do serviço "demo" (nginx:alpine), inclusive a
primeira, sem precisar de bootstrap manual.

### Onde olhar

- **Proxy do serviço demo**: `http://localhost:8095`
- **Health/métricas do group**: `http://localhost:9095/healthz`,
  `/readyz`, `/metrics`
- **Grafana**: `http://localhost:3000` (dashboard "Autoscaler" já
  provisionado — métricas de réplicas, CPU, scale up/down, e logs)
- **Prometheus**: `http://localhost:9091` (confira em Status → Targets que
  `group-demo` está `UP`)

### Portas 8095/9095 (não 8090/9090)

Ajustadas pra não colidir com outro projeto rodando no mesmo host — se não
for o teu caso, pode simplificar de volta pra 8090/9090 em
`demo/docker-compose.yml`.

## Auth + portal (auth gerido pelo autoscaler)

Não precisa da plataforma de observabilidade — fica numa rede própria
(`auth-net`, nome fixo) dentro do mesmo `docker-compose.yml`. A diferença
para uma instalação "normal" do `auth` (ver
[services/auth/docker-compose.yml](../services/auth/docker-compose.yml)):
aqui não existe um serviço Compose `authd` — quem cria/escala/remove os
containers do auth é o **`group-authd`**, uma instância do autoscaler
(mesmo binário do `group-demo`, outro `TARGET_SERVICE`), a partir de
[launch-template.auth.json](launch-template.auth.json). É o autoscaler a
gerir um serviço real, não só o nginx de exemplo.

1. Construa as imagens:
   ```sh
   docker build -t autoscaler-group:latest services/autoscaler
   docker build -t auth-service:latest services/auth
   docker build -t portal:latest services/portal
   ```
2. Suba:
   ```sh
   docker compose -f demo/docker-compose.yml up -d auth-postgres group-authd portal
   ```
   O `group-authd` cria a primeira réplica do `authd` sozinho (mesmo
   princípio do `group-demo`) — confira com
   `docker ps --filter label=autoscaler.service=auth`.
3. Abra `http://localhost:3100/register`, cria uma conta.
4. Promove essa conta a super-admin (só necessário a primeira vez).
   Como as réplicas do auth agora nascem do `launch-template.auth.json`
   (um ficheiro estático, sem substituição de variáveis do Compose), o
   fluxo é: editar o `env` do template, e reiniciar o `group-authd` —
   ele remove as réplicas que geriu como parte do seu próprio shutdown
   (não precisa matar a réplica à mão) e recria a partir do template já
   atualizado:
   ```sh
   # edita demo/launch-template.auth.json, adiciona ao array "env":
   #   "AUTH_BOOTSTRAP_SUPERADMIN_EMAIL=teu-email@example.test"
   docker restart demo-group-authd-1
   ```
   Depois de confirmar que a conta foi promovida (log
   `"conta promovida a super-admin (bootstrap)"` no `docker logs` da
   réplica nova), reverte o `env` do template — a variável só precisa
   estar presente no momento da promoção, não depois.
5. Entra de novo em `http://localhost:3100/login` — agora aparece
   "Utilizadores" no menu, e `/admin/users` deixa criar/listar/promover
   outras contas.

### Onde olhar

- **Portal**: `http://localhost:3100`
- **Proxy do group-authd** (o que era "o auth service" diretamente,
  agora na frente de 1+ réplicas): `http://localhost:8081` (ex:
  `curl http://localhost:8081/.well-known/jwks.json`)
- **Health/métricas do group-authd**: `http://localhost:9081/healthz`

### Uma réplica só por padrão, e por quê

`group-authd` sobe com `MIN_REPLICAS=1`. Não é arbitrário: se duas
réplicas nascessem ao mesmo tempo contra um volume `auth-signing-key`
ainda vazio, cada uma tentaria gerar a sua própria chave de assinatura
RSA (ver `internal/keystore.LoadOrGenerate` em `services/auth`) — uma
corrida que faria réplicas emitirem/validarem tokens com chaves
diferentes. Depois da chave já existir no volume (ou seja, depois da
primeira réplica ter subido pelo menos uma vez), subir `MAX_REPLICAS`
(hoje `3`) e deixar o autoscaler escalar por CPU é seguro — todas as
réplicas novas só leem a chave já existente.

## Derrubar

```sh
docker compose -f demo/docker-compose.yml down
```

Cada group (`group-demo`, `group-authd`) remove as réplicas que criou
como parte do seu shutdown gracioso — não fica nada órfão. Dados do auth
(contas, chaves) ficam em volumes nomeados e sobrevivem a este comando:
- `auth-postgres-data` — declarado no Compose, `down -v` remove.
- `auth-signing-key` — **não** é gerido pelo Compose (nenhum serviço
  Compose o referencia; é criado pelo Docker na hora em que a primeira
  réplica do `group-authd` o monta, via `binds` do launch template) —
  `down -v` não o toca. Para limpar de verdade: `docker volume rm
  auth-signing-key`.

A plataforma de observabilidade (`services/monitoring`) não é afetada;
derrube-a separadamente se quiser:
```sh
docker compose -f services/monitoring/docker-compose.yml down
```
