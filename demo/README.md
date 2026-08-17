# demo

Demo end-to-end dos serviços BASE do base-stack:
[services/auth](../services/auth) + [services/portal](../services/portal) +
[services/database](../services/database) +
[services/secretsadmin](../services/secretsadmin) +
[services/templatesadmin](../services/templatesadmin) +
[services/launcher](../services/launcher), com o próprio `auth` gerido pelo
autoscaler (`group-authd`), não como um container estático, e o Postgres do
auth vindo do `database` (Postgres partilhado com backups/PITR), não de um
`postgres:16-alpine` ad-hoc só deste demo.

Isto é a PLATAFORMA -- depois de subir, novos serviços não se escrevem à
mão neste `docker-compose.yml`: criam-se um modelo no templatesadmin e
lançam-se a partir dele via launcher (ver "Criar um novo serviço
autoscalado" e "Lançar containers sem autoscaling (launcher)" abaixo).
[services/monitoring](../services/monitoring) (Prometheus/Grafana) é uma
plataforma separada e opcional que qualquer serviço criado a partir daqui
já sabe anunciar-se a (labels `prometheus.scrape`/`prometheus.port`).

## Subir a plataforma base (auth gerido pelo autoscaler, Postgres partilhado)

Fica numa rede própria (`auth-net`, nome fixo) dentro do mesmo
`docker-compose.yml`. Duas diferenças em relação a uma instalação "normal"
do `auth` (ver
[services/auth/docker-compose.yml](../services/auth/docker-compose.yml)):

- Não existe um serviço Compose `authd` — quem cria/escala/remove os
  containers do auth é o **`group-authd`**, uma instância do autoscaler, a
  partir de [launch-template.auth.json](launch-template.auth.json) (pedindo
  cada réplica nova ao **`launcher`**, ver "Segredos" abaixo).
- O Postgres não é um `postgres:16-alpine` dedicado ao auth — é o
  **`database`** ([services/database](../services/database)), o Postgres
  partilhado do base-stack (um schema por serviço, backups em rotina +
  PITR via pgBackRest). `demo/db-init/*.sql` provisiona a role de cada
  serviço nele na primeira inicialização; cada serviço cria o seu próprio
  schema sozinho no arranque (ver `internal/store/migrate.go` de cada um)
  — o `database` não precisa saber nada sobre o schema de ninguém.

1. Construa as imagens:
   ```sh
   docker build -t autoscaler-group:latest services/autoscaler
   docker build -t auth-service:latest services/auth
   docker build -t portal:latest services/portal
   docker build -t database-service:latest services/database
   docker build -t secretsadmin:latest services/secretsadmin
   docker build -t templatesadmin:latest services/templatesadmin
   docker build -t launcher:latest services/launcher
   ```
2. Suba tudo:
   ```sh
   docker compose -f demo/docker-compose.yml up -d
   ```
   O `group-authd` cria a primeira réplica do `authd` sozinho (pedindo-a
   ao `launcher`) — confira com
   `docker ps --filter label=autoscaler.service=auth`.

   Opcional: `docker compose -f demo/docker-compose.yml up -d adminer`
   sobe também um browser de tabelas/SQL ad-hoc contra o `database` (ver
   "Onde olhar" abaixo) — só para inspecionar dados em dev.
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
- **Adminer** (se subiu o serviço opcional): `http://localhost:8096` —
  servidor `database`, utilizador/senha/BD conforme
  `services/database/docker-compose.yml` (aqui: `app`/`app`/`app`).
  Backups/PITR do schema `auth` seguem o mesmo fluxo documentado em
  [services/database/README.md](../services/database/README.md), só
  trocando `-f services/database/docker-compose.yml` por
  `-f demo/docker-compose.yml` nos comandos.

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

## Segredos (secretsadmin) e a conta de serviço do autoscaler

[services/secretsadmin](../services/secretsadmin) guarda valores cifrados
(AES-256-GCM) referenciados por nome dentro do `env` de um launch template
via `${secret:NOME}` — ex: em vez de
`"AUTH_DATABASE_URL=postgres://auth:auth@database:5432/app"` (senha em
claro no ficheiro), `"AUTH_DATABASE_URL=postgres://auth:${secret:auth-db-password}@database:5432/app"`.
Humanos (role `infra-admin`, via `/admin/secrets` no portal) gerem
nomes/valores mas nunca conseguem voltar a ler um valor depois de o
gravarem; só uma conta de MÁQUINA (role `service`) consegue resolver um
nome para o valor em claro (`POST /v1/secrets/resolve`), e só o
[services/launcher](../services/launcher) faz isso -- é ele quem, de
facto, cria todo container novo (ver "Lançar containers sem autoscaling
(launcher)" abaixo), na altura de cada scale up pedido por um group ou de
cada instância "solo" pedida via `POST /v1/instances`.

1. Construa e suba o secretsadmin e o launcher:
   ```sh
   docker build -t secretsadmin:latest services/secretsadmin
   docker build -t launcher:latest services/launcher
   docker compose -f demo/docker-compose.yml up -d secretsadmin launcher
   ```
2. Bootstrap da conta de serviço (uma vez só). Como `super-admin`, abre
  `/admin/users`, cria um utilizador com role `service` e depois usa
  **Gerar tokens** na respetiva linha. Guarda o `refresh_token` mostrado
  uma única vez; ao criar um autoscaler em `/admin/autoscaler/new`, cola-o
  no campo de token da conta de serviço. O launcher precisa da sua própria
  conta `service` para falar com o secretsadmin, e cada group precisa de
  OUTRA conta `service` para falar com o launcher. Emitir um novo par não
  revoga automaticamente pares anteriores.

  O mesmo fluxo também pode ser feito diretamente contra o auth service:
   ```sh
   # 1. Regista a conta (role nasce "user")
   curl -X POST http://localhost:8081/v1/register \
     -H "Content-Type: application/json" \
     -d '{"email":"svc-launcher@service.test","password":"não-vai-ser-usada"}'
   # 2. Promove a "service" (precisa de um super-admin já existente --
   #    ver "promoção a super-admin" mais acima)
   curl -X PATCH http://localhost:8081/v1/admin/users/<ID>/role \
     -H "Authorization: Bearer <TOKEN_SUPER_ADMIN>" -H "Content-Type: application/json" \
     -d '{"role":"service"}'
   # 3. Emite o par access+refresh token (só um super-admin pode)
   curl -X POST http://localhost:8081/v1/admin/users/<ID>/tokens \
     -H "Authorization: Bearer <TOKEN_SUPER_ADMIN>"
   ```
  Guarda o `refresh_token` da resposta -- é o `LAUNCHER_SECRETS_REFRESH_TOKEN`
  do launcher. Os tokens emitidos pela interface seguem exatamente o mesmo
  contrato:
   ```sh
   export LAUNCHER_SECRETS_REFRESH_TOKEN="<refresh_token>"
   docker compose -f demo/docker-compose.yml up -d launcher
   ```
   Repita os 3 passos com outra conta (ex: `svc-group-authd@service.test`)
   para obter o `GROUP_AUTHD_LAUNCHER_REFRESH_TOKEN` de `group-authd` (ver
   serviço `group-authd` em `demo/docker-compose.yml`):
   ```sh
   export GROUP_AUTHD_LAUNCHER_REFRESH_TOKEN="<refresh_token>"
   docker compose -f demo/docker-compose.yml up -d group-authd
   ```
3. Cria um segredo via portal (`/admin/secrets`, role `infra-admin`), cria
   um modelo no templatesadmin (`/admin/templates`) referenciando-o em
   `${secret:NOME}` dentro do `env`, e lança-o como instância "solo" via
   launcher (ver "Lançar containers sem autoscaling (launcher)" abaixo).
   Confirma que resolveu de verdade:
   ```sh
   docker inspect <container_id_devolvido_pelo_launcher> \
     --format '{{range .Config.Env}}{{println .}}{{end}}' | grep NOME_DA_VARIAVEL
   ```

### Cuidado real: `group-authd` continua auto-referencial, só que agora sempre

Antes desta feature, o cuidado abaixo só se aplicava se
`launch-template.auth.json` referenciasse `${secret:...}` (não referencia,
por isso `group-authd` nunca sofria o problema na prática). Com o
launcher, o cuidado passa a valer INCONDICIONALMENTE, porque toda réplica
nova -- com ou sem segredo -- agora depende de `group-authd` conseguir
chamar o launcher, autenticado com o seu próprio `LAUNCHER_REFRESH_TOKEN`.

`group-authd` renova esse token chamando `.../v1/token/refresh` através do
seu PRÓPRIO proxy (é ele quem serve o auth service que gere). Se as
réplicas do `auth` caírem a zero (ex: depois de um `POST /v1/restart`, que
termina TODAS as réplicas antes de recriar, ou um reboot completo do
ambiente), renovar o token falha -- e sem token, `group-authd` não
consegue pedir ao launcher a primeira réplica nova. Deadlock: a réplica
nunca sobe, porque conseguir criá-la depende de uma réplica já estar de
pé. Isto NÃO é um problema para outros groups (cujo `AUTH_SERVICE_URL`
aponta a um auth service independente, já de pé) -- só para `group-authd`
especificamente, por gerir o próprio auth service de que ele mesmo
depende, e acontece de novo a cada vez que reinicia do zero, não só uma
vez.

Resolvido via `ALLOW_COLD_START_FALLBACK=true` (só em `group-authd`, ver
`docker-compose.yml`): quando o pedido ao launcher falha E não há nenhuma
réplica viva, `group-authd` cria essa réplica localmente, uma única vez
-- o suficiente para o seu próprio proxy voltar a ter um backend, o token
voltar a renovar, e a partir da réplica seguinte voltar a pedir sempre ao
launcher, como qualquer outro group (ver `applyScaleUp` em
`services/autoscaler/cmd/group/main.go`). Esta válvula de escape fica
desligada por defeito em qualquer group novo -- só é ligada aqui porque
`group-authd` é o único auto-referencial.

## Criar um novo serviço autoscalado (templatesadmin)

[services/templatesadmin](../services/templatesadmin) é um repositório de
launch templates: guarda, por nome, só o que o CONTAINER da aplicação
precisa -- imagem, `cmd`, `env`, `labels`, `binds`, `network`,
`extraHosts`. Não tem nenhum campo de autoscaling (réplicas, thresholds de
CPU, nome do serviço, portas) -- isso é decidido no momento de lançar,
não faz parte do modelo (ver abaixo). Não sobe nem gere nenhum container
por si só -- quem faz isso é o launcher.

1. Construa e suba o templatesadmin:
   ```sh
   docker build -t templatesadmin:latest services/templatesadmin
   docker compose -f demo/docker-compose.yml up -d templatesadmin
   ```
2. No portal (`/admin/templates`, role `infra-admin`): escolha uma
   imagem já buildada/pulled no Docker do host (populado via
   `GET /v1/images`, que fala com o Engine API local -- por isso este
   serviço também monta `/var/run/docker.sock`, mesma superfície de
   risco que `group-authd` já tem) e preencha as variáveis de ambiente
   (estáticas ou `${secret:NOME}`, escolhendo um nome já criado em
   `/admin/secrets`).
3. Salve o modelo com um `name` (ex: `laravel-app`) -- não precisa copiar
   nada para `demo/docker-compose.yml`: o launcher lê este modelo direto
   do templatesadmin pelo nome, no momento de lançar.
4. Lance-o a partir do modelo:
   - **Instância solo** (só a app, sem autoscaling nenhum) -- em
     `/admin/launcher`, escolha o modelo e clique "Lançar".
   - **Autoscaler-group** (a app gerida por um autoscaler, que passa a
     criar/remover as suas próprias réplicas) -- em `/admin/autoscaler`,
     secção "Criar grupo": escolha o modelo e preencha o nome do serviço,
     réplicas mín./máx. e thresholds de CPU (é aqui, não no modelo, que
     esta config vive agora).

Ver "Lançar containers sem autoscaling (launcher)" abaixo para os
detalhes de cada rota da API por trás destes dois botões.

Um group criado em "Criar grupo" aparece sozinho na tabela de
`/admin/autoscaler` -- essa lista é descoberta em tempo real contra o
Docker (via launcher, `GET /v1/groups`, por label `autoscaler.role=group`),
não uma configuração estática. A partir dessa tabela também se consegue
lançar/matar réplicas específicas de cada group manualmente (`POST`/`DELETE
/v1/replicas` na API admin desse group), além da policy e do restart que
já existiam.

## Lançar containers sem autoscaling (launcher)

[services/launcher](../services/launcher) é o serviço que resolve o
problema que motivou toda esta secção de segredos: antes, a ÚNICA forma
de uma aplicação ter acesso a `${secret:NOME}` era correr sob um
autoscaler-group, porque só o `cmd/group` sabia falar com o secretsadmin.
Agora é o launcher quem sabe fazer isso -- e ele consegue lançar dois
tipos de coisa a partir de um modelo já guardado no templatesadmin:

- `"kind": "solo"` -- um único container da aplicação, sem nenhum
  autoscaler à volta, com o `env` já resolvido. UI: `/admin/launcher`.
- `"kind": "group"` -- um `autoscaler-group` inteiro (a mesma imagem de
  `group-authd`), que a partir daí passa a gerir as suas PRÓPRIAS
  réplicas chamando o launcher (ver "Cuidado real" acima para a conta de
  serviço que esse group novo precisa). UI: `/admin/autoscaler`, secção
  "Criar grupo" -- é aqui, não no modelo, que se preenche o nome do
  serviço, réplicas mín./máx. e thresholds de CPU.

Nenhum dos dois passa pelo `demo/docker-compose.yml` -- é exatamente o
"lançar um container fora do compose" que motivou este serviço. Ambas as
UIs são só um proxy fino para a API do launcher (ver
`services/portal/lib/launcherClient.ts`); o mesmo dá para fazer direto
por `curl`:

```sh
# Lançar uma instância solo a partir do modelo "demo" (já criado no
# templatesadmin -- ver secção anterior):
curl -X POST http://localhost:8094/v1/instances \
  -H "Authorization: Bearer <TOKEN_INFRA_ADMIN>" -H "Content-Type: application/json" \
  -d '{"templateName":"demo","kind":"solo"}'

# Lançar um group a partir do mesmo modelo:
curl -X POST http://localhost:8094/v1/instances \
  -H "Authorization: Bearer <TOKEN_INFRA_ADMIN>" -H "Content-Type: application/json" \
  -d '{"templateName":"demo","kind":"group","targetService":"demo","minReplicas":1,"maxReplicas":3,"cpuScaleUpPercent":50,"cpuScaleDownPercent":20,"replicaAuthToken":"<REFRESH_TOKEN_SERVICE>"}'

# Listar (só o que este launcher lançou -- solo/group) e as réplicas de
# QUALQUER group (descobertas contra o Docker, inclusive as que o group
# criou por conta própria, sem nunca passar por aqui):
curl http://localhost:8094/v1/instances -H "Authorization: Bearer <TOKEN_INFRA_ADMIN>"
curl http://localhost:8094/v1/replicas -H "Authorization: Bearer <TOKEN_INFRA_ADMIN>"

# Parar (sem remover) ou eliminar QUALQUER container desta plataforma pelo
# seu containerId -- funciona para uma instância solo ou uma réplica,
# nunca para o processo de um group inteiro (isso é sempre
# DELETE /v1/groups/{containerId}, feito a partir de /admin/autoscaler):
curl -X POST http://localhost:8094/v1/containers/<containerId>/stop -H "Authorization: Bearer <TOKEN_INFRA_ADMIN>"
curl -X DELETE http://localhost:8094/v1/containers/<containerId> -H "Authorization: Bearer <TOKEN_INFRA_ADMIN>"
```

Note que `launcher` não publica porta no host em `demo/docker-compose.yml`
(mesmo espírito de `secretsadmin`/`templatesadmin`) -- os `curl` acima só
funcionam de dentro da rede `auth-net` (ex: de outro container), ou
publicando `8094:8094` temporariamente para testar do host. É por isso
que o portal (que já está nessa rede) fala com o launcher via
`LAUNCHER_SERVICE_URL`, não via `localhost`.

## Acesso externo via browser (Traefik)

Nenhum container publica porta própria no host -- inclusive uma instância
"solo" ou o proxy de um `group`, mesmo depois desta secção. Em vez disso, o
`demo/docker-compose.yml` sobe um serviço `traefik`, o único a publicar uma
porta (80), que decide para onde reencaminhar cada pedido pelo `Host:` do
pedido, lendo labels que o launcher escreve no momento de criar o
container (nunca depois -- expor uma instância já viva exige recriá-la,
mesmo limite que já existe para imagem/env).

Para expor algo, passe `"exposeAs": "<slug>"` ao criar (UI:
`/admin/launcher`, "Expor publicamente como"; ou `/admin/autoscaler/new`,
mesmo campo, para um group). Uma instância `solo` também precisa de
`"exposePort"` -- a porta que a APLICAÇÃO escuta lá dentro do container,
já que a imagem é arbitrária e não há convenção nenhuma sobre isso. Um
`group` não pede porta: expõe sempre o seu próprio proxy interno (porta
`8090`), nunca uma réplica diretamente.

```sh
curl -X POST http://localhost:8094/v1/instances \
  -H "Authorization: Bearer <TOKEN_INFRA_ADMIN>" -H "Content-Type: application/json" \
  -d '{"templateName":"demo","kind":"solo","exposeAs":"minha-app","exposePort":80}'
```

O host completo fica `<slug>.PUBLIC_BASE_DOMAIN` (env var do `launcher`,
ver `docker-compose.yml`). Em dev, o default é `127.0.0.1.nip.io` -- um
serviço de DNS público real que resolve QUALQUER
`algo.127.0.0.1.nip.io` para `127.0.0.1`, sem precisar editar `/etc/hosts`
nem ter domínio próprio. Depois de criar a instância do exemplo acima,
basta abrir `http://minha-app.127.0.0.1.nip.io` no browser -- chega à
app através do Traefik, na porta 80. Em produção, sobreponha
`PUBLIC_BASE_DOMAIN` com o domínio real (e, nesse caso, aponte um registo
DNS wildcard `*.dominio.com` para o IP do host).

A regra do Traefik para cada instância exposta casa tanto o host exato
(`<slug>.PUBLIC_BASE_DOMAIN`) quanto qualquer subdomínio na frente dele
(`<qualquer-coisa>.<slug>.PUBLIC_BASE_DOMAIN`) -- ambos chegam ao MESMO
container. É assim que uma app multi-tenant expõe um subdomínio por
tenant sem o launcher saber a lista de tenants nem precisar recriar nada
quando um tenant novo surge: `http://aplus.minha-app.127.0.0.1.nip.io`
(nip.io já resolve qualquer subdomínio extra para `127.0.0.1` sem
configuração nenhuma) chega à mesma app que `http://minha-app.127.0.0.1.nip.io`,
e é a própria aplicação -- não o launcher nem o Traefik -- quem lê o
`Host:` do pedido e escolhe o tenant (`aplus`) a partir do rótulo mais à
esquerda.

Uma instância exposta numa rede personalizada (ver `/admin/networks`)
também funciona: sempre que uma rede nova é criada, o launcher liga o
Traefik a ela automaticamente. Para uma rede já existente antes desta
feature, ligue o Traefik manualmente pela própria UI de `/admin/networks`
("ligar container").

## Derrubar

```sh
docker compose -f demo/docker-compose.yml down
```

Cada group (`group-authd`, e qualquer outro group que você tenha lançado
via launcher) remove as réplicas que criou como parte do seu shutdown
gracioso — não fica nada órfão. Dados do auth
(contas, chaves) ficam em volumes nomeados e sobrevivem a este comando:
- `demo-database-data` / `demo-database-backup-repo` — declarados no
  Compose, `down -v` remove os dois (isto apaga também os backups locais
  do pgBackRest -- se quiseres preservar backups ao derrubar o ambiente
  de demo, não uses `-v`, ou troca `PGBACKREST_REPO_TYPE` pra `s3` antes).
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
