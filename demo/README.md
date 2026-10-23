# demo

Demo end-to-end dos serviços BASE do base-stack:
[services/auth](../services/auth) + [services/portal](../services/portal) +
[services/database](../services/database) +
[services/secretsadmin](../services/secretsadmin) +
[services/templatesadmin](../services/templatesadmin) +
[services/launcher](../services/launcher), com o Postgres do auth vindo do
`database` (Postgres partilhado com backups/PITR), não de um
`postgres:16-alpine` ad-hoc só deste demo.

Isto é a PLATAFORMA -- depois de subir, novos serviços não se escrevem à
mão neste `docker-compose.yml`: criam-se um modelo no templatesadmin e
lançam-se a partir dele via launcher (ver "Criar um novo serviço
autoscalado" e "Lançar containers sem autoscaling (launcher)" abaixo).
[services/monitoring](../services/monitoring) (Prometheus/Grafana/Loki) é
uma plataforma à parte, pensada para servir vários projetos ao mesmo tempo
(não só este demo) -- mas o `traefik` deste `docker-compose.yml` já nasce
ligado à rede dela (`observability-net`, externa), por isso **tem de estar
de pé primeiro** -- ver "Subir a plataforma base" abaixo e
"Observabilidade" mais à frente.

## Subir a plataforma base (Postgres partilhado)

Fica numa rede própria (`auth-net`, nome fixo) dentro do mesmo
`docker-compose.yml`. O `auth` é um container estático, como o resto do
plano de controlo (já foi um autoscaler-group, mas isso criava um deadlock
no arranque a frio -- ver o comentário do serviço `auth` no compose). A
diferença em relação a uma instalação "normal" do `auth` (ver
[services/auth/docker-compose.yml](../services/auth/docker-compose.yml)):

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
2. Suba a plataforma de observabilidade primeiro (uma vez só -- ver
   [services/monitoring/README.md](../services/monitoring/README.md)) e só
   depois este demo:
   ```sh
   docker compose -f services/monitoring/docker-compose.yml up -d
   docker compose -f demo/docker-compose.yml up -d
   ```
   Nessa ordem porque o `traefik` deste ficheiro já nasce ligado à rede
   `observability-net` (externa) -- se tentares subir o demo primeiro,
   `docker compose` recusa com algo como
   `network observability-net declared as external, but could not be found`.

   Opcional: `docker compose -f demo/docker-compose.yml up -d adminer`
   sobe também um browser de tabelas/SQL ad-hoc contra o `database` (ver
   "Onde olhar" abaixo) — só para inspecionar dados em dev.
3. Abra `http://localhost:3100/register`, cria uma conta.
4. Promove essa conta a super-admin (só necessário a primeira vez):
   ```sh
   AUTH_BOOTSTRAP_SUPERADMIN_EMAIL=teu-email@example.test \
     docker compose -f demo/docker-compose.yml up -d auth
   ```
   Confirma o log `"conta promovida a super-admin (bootstrap)"` em
   `docker compose -f demo/docker-compose.yml logs auth`. A variável só
   precisa de estar presente nesse arranque; os seguintes podem ir sem ela.
5. Entra de novo em `http://localhost:3100/login` — agora aparece
   "Utilizadores" no menu, e `/admin/users` deixa criar/listar/promover
   outras contas.

### Onde olhar

- **Portal**: `http://localhost:3100`
- **Auth service**: `http://localhost:8081` (ex:
  `curl http://localhost:8081/.well-known/jwks.json`,
  `curl http://localhost:8081/readyz`)
- **Adminer** (se subiu o serviço opcional): `http://localhost:8096` —
  servidor `database`, utilizador/senha/BD conforme
  `services/database/docker-compose.yml` (aqui: `app`/`app`/`app`).
  Backups/PITR do schema `auth` seguem o mesmo fluxo documentado em
  [services/database/README.md](../services/database/README.md), só
  trocando `-f services/database/docker-compose.yml` por
  `-f demo/docker-compose.yml` nos comandos.

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
  conta `service` para falar com o secretsadmin, e cada group que lances
  precisa de OUTRA conta `service` para falar com o launcher. Emitir um novo par não
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
3. Cria um segredo via portal (`/admin/secrets`, role `infra-admin`), cria
   um modelo no templatesadmin (`/admin/templates`) referenciando-o em
   `${secret:NOME}` dentro do `env`, e lança-o como instância "solo" via
   launcher (ver "Lançar containers sem autoscaling (launcher)" abaixo).
   Confirma que resolveu de verdade:
   ```sh
   docker inspect <container_id_devolvido_pelo_launcher> \
     --format '{{range .Config.Env}}{{println .}}{{end}}' | grep NOME_DA_VARIAVEL
   ```

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
   `GET /v1/images`, que lê o Docker pelo proxy só de leitura -- ver
   "Acesso ao Docker" abaixo) e preencha as variáveis de ambiente
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
- `"kind": "group"` -- um `autoscaler-group` inteiro (imagem
  `autoscaler-group:latest`), que a partir daí passa a gerir as suas
  PRÓPRIAS réplicas chamando o launcher (ver "Segredos" acima para a conta
  de serviço que esse group novo precisa). UI: `/admin/autoscaler`, secção
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

### HTTPS/TLS (ACME)

`demo/docker-compose.yml` já sobe o Traefik com um segundo entrypoint
(`websecure`, porta 443) e um resolver ACME chamado `le` (Let's Encrypt),
mas **desligado por padrão**: nenhum router usa `le` até o `launcher` ser
configurado para escrevê-lo. Isto é intencional -- ligar por padrão num
ambiente de dev (`127.0.0.1.nip.io`, loopback) não serviria de nada, já
que o challenge HTTP-01 da Let's Encrypt precisa de alcançar o teu host
pela internet na porta 80 para confirmar que o domínio é mesmo teu.

Para ligar em produção (host com IP público, porta 80 já acessível de
fora -- a mesma que o Traefik já usa hoje):

1. Aponte `PUBLIC_BASE_DOMAIN` para um domínio real, com um registo `A`
   (ou `*.dominio.com` para o caso multi-tenant) a apontar para o IP
   público do host -- ou use `<IP-público>.nip.io`, que também é um
   domínio real do ponto de vista da Let's Encrypt (só `127.0.0.1.nip.io`,
   loopback, não funciona).
2. Defina `TRAEFIK_ACME_EMAIL` (contacto exigido pela Let's Encrypt para
   avisos de expiração) e suba `traefik` de novo:
   ```sh
   export TRAEFIK_ACME_EMAIL="teu-email@example.com"
   docker compose -f demo/docker-compose.yml up -d traefik
   ```
3. Confirme contra a CA de **staging** primeiro (default de
   `TRAEFIK_ACME_CA_SERVER` -- sem limite de taxa realista, mas emite
   certificados em que o browser não confia) expondo uma instância e
   verificando `docker logs demo-traefik-1` por erros de challenge.
4. Só depois de confirmar, mude para a CA de produção e refaça o passo 2:
   ```sh
   export TRAEFIK_ACME_CA_SERVER="https://acme-v02.api.letsencrypt.org/directory"
   docker compose -f demo/docker-compose.yml up -d traefik
   ```
5. Ligue o `launcher` para passar a escrever HTTPS em toda instância
   exposta a partir daqui:
   ```sh
   export PUBLIC_TLS_CERT_RESOLVER=le
   docker compose -f demo/docker-compose.yml up -d launcher
   ```

Só instâncias expostas **depois** deste último passo ganham HTTPS -- uma
já exposta antes continua só em HTTP até ser recriada (mesma limitação já
documentada acima para imagem/env: expor é decidido na criação, não
depois). O certificado fica em `acme.json`, dentro do volume nomeado
`traefik-acme` -- sobrevive a um `docker compose down` normal (sem `-v`).

### Observabilidade (Prometheus/Grafana/Loki)

O `traefik` já sobe com access logs em JSON (`--accesslog`), um endpoint
`/metrics` Prometheus nativo num entrypoint próprio (`:8082`, nunca
publicado no host), e já nasce ligado à rede `observability-net` (ver
serviço `traefik` em `demo/docker-compose.yml`) -- nada a configurar à
parte, desde que a [plataforma de observabilidade](../services/monitoring)
já esteja de pé (ver "Subir a plataforma base" acima, é pré-requisito
deste `docker-compose.yml` inteiro, não só da observabilidade).

- **Logs**: automático -- o Promtail já coleta o stdout de QUALQUER
  container do host, `traefik` incluído, sem precisar de rede nem label
  nenhuma.
- **Métricas**: automático -- o Prometheus descobre o `traefik` sozinho
  via `docker_sd_configs` (labels `prometheus.scrape`/`prometheus.port`,
  já na label do serviço) e alcança-o pelo nome (`traefik:8082`, resolvido
  via DNS interno do Docker dentro de `observability-net`).
- Abra o Grafana (`http://localhost:3000` por defeito, ver
  `services/monitoring/README.md`) -- o dashboard "Traefik (gateway)", na
  pasta "Base Stack", já vem provisionado: requisições/erros/latência por
  instância exposta (`traefik_service_requests_total`/`_duration_seconds`,
  rotuladas pelo mesmo nome que `exposeAs` gerou) mais os access logs no
  fim.

Um serviço lançado depois via templatesadmin/launcher segue a mesma
convenção (`prometheus.scrape`/`prometheus.port` no seu `env`/labels, se
quiser aparecer aqui também) -- ver `services/monitoring/README.md`,
"Como um projeto novo aparece aqui".

## Acesso ao Docker

Só o **launcher** fala com o socket do Docker em modo escrita. Montar o
socket com `:ro` não protege nada (a API continua toda acessível), por isso
quem só precisa de ler (Traefik, templatesadmin, e Prometheus/Promtail em
`services/monitoring`) passa por um `docker-socket-proxy` que só deixa
passar GETs de containers, redes e imagens, numa rede `internal` própria
(`docker-api-ro`). Essa rede não tem a label `base-stack.managed`, por isso
não aparece em `/admin/networks`: o proxy deixa ler o `Env` de todos os
containers, e nenhuma app deve chegar lá.

Tudo o que o launcher cria a partir de um modelo ou de um pedido de réplica
passa por uma política (`services/launcher/internal/httpapi/specpolicy.go`):

- `binds` com caminhos do host só dentro de `LAUNCHER_ALLOWED_BIND_PATHS`
  (vazio por defeito, ou seja, só volumes nomeados). `/`, `/etc`, `/proc`,
  `/sys`, `/dev`, `/run`, `/var/run`, `/var/lib/docker`, `/root`, `/boot` e
  qualquer `docker.sock` ficam sempre proibidos.
- Volumes nomeados criados por uma stack compose (ex: `demo-database-data`)
  não podem ser montados por uma app.
- `network: host` e `network: container:<id>` são recusados.
- Todo container de app corre com `no-new-privileges`.

Um modelo que monte código-fonte do host (ex:
`services/autoscaler/launch-template.laravel-web.example.json`) precisa
desse caminho em `LAUNCHER_ALLOWED_BIND_PATHS`.

**Ainda por resolver:** cada autoscaler-group lançado pelo launcher continua com o socket direto, para listar, inspecionar e
parar as suas réplicas. Isto desaparece quando o scaling passar para o
reconciliador central do plano de controlo, em vez de um container por
group.

## Derrubar

```sh
make demo-down
```

Só `docker compose down` **não chega**: desligar a plataforma (launcher)
já não derruba as aplicações. Os groups e instâncias lançados
pelo launcher, e as réplicas de todos os groups, ficam a correr. É de
propósito: um reboot do host ou um restart do launcher não deve apagar
nada, e tudo tem `RestartPolicy: unless-stopped`. `make demo-down` faz o
`down` e depois remove todo container com a label `base-stack.managed=true`
que tenha sobrado (e as redes com a mesma label). Para apagar só uma
aplicação, usa "Parar grupo" em `/admin/autoscaler` (ou
`DELETE /v1/groups/{id}`), que remove o group e as réplicas dele. Dados do auth
(contas, chaves) ficam em volumes nomeados e sobrevivem a este comando:
- `demo-database-data` / `demo-database-backup-repo` — declarados no
  Compose, `down -v` remove os dois (isto apaga também os backups locais
  do pgBackRest -- se quiseres preservar backups ao derrubar o ambiente
  de demo, não uses `-v`, ou troca `PGBACKREST_REPO_TYPE` pra `s3` antes).
- `auth-signing-key` — a chave de assinatura dos tokens; `down -v` também
  o remove, e todo token já emitido deixa de validar.

A plataforma de observabilidade (`services/monitoring`) não é afetada;
derrube-a separadamente se quiser:
```sh
docker compose -f services/monitoring/docker-compose.yml down
```
