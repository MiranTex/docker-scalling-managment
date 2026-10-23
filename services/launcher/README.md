# launcher

O serviço que cria containers. Tem dois papéis:

- **API de plataforma** (`/v1/projects`): projetos, ambientes, serviços,
  addons, deploys, domínios, segredos, jobs e tokens de CI. É o modelo
  principal e o que este README descreve.
- **Fluxo antigo** (`/v1/instances`, `/v1/groups`, `/v1/replicas`,
  `/v1/networks`): instâncias "solo" e autoscaler-groups lançados a partir
  dos modelos do templatesadmin. Continua a funcionar, mas não é isolado
  por projeto. Ver `demo/README.md`.

É o único serviço da plataforma com o socket do Docker em modo escrita.

## Modelo

```
Conta (só "default" por agora)
 └─ Projeto              quotas de vCPU/memória, registries privados, tokens de deploy
     └─ Ambiente         production, staging... -- rede Docker própria (bs-<projeto>-<ambiente>)
         ├─ Serviços     web (recebe tráfego) ou worker; revisões imutáveis; scaling
         ├─ Addons       Postgres 17 / Redis, cada um num container com volume próprio
         ├─ Segredos     cifrados (AES-256-GCM), usados como ${secret:NOME}
         ├─ Domínios     por serviço web, ativos só depois de o DNS apontar para aqui
         └─ Jobs         migrations, backups, restores, comandos pontuais
```

**Isolamento.** Cada ambiente tem a sua rede. Os containers de um ambiente
só estão nessa rede, e só o Traefik e o launcher se ligam a todas. Um
serviço chega a outro do mesmo ambiente por `http://<slug>:<porta>`, e a
uma base de dados por `<slug-do-addon>:5432`. Nada de um projeto vê outro,
nem a plataforma.

**Serviços e revisões.** O serviço guarda a configuração para o próximo
deploy: env, comando, release, recursos, porta e health check. Cada deploy
congela essa configuração com uma imagem numa revisão numerada. Um
rollback é voltar a pôr uma revisão anterior como atual. O scaling (fixo ou
por CPU) aplica-se logo, sem deploy.

**Env de um container de serviço**, por ordem (o último ganha):
`PORT`, as URLs dos addons do ambiente (ex.: `DATABASE_URL`, `REDIS_URL`)
e o env do serviço com os `${secret:NOME}` resolvidos.

## Deploy

```
pull (credenciais do projeto) → release (migrations, como job) → N réplicas novas
→ todas passam o health check → troca a revisão atual e o routing → drena e pára as antigas
```

- **Health check:** caminho HTTP (resposta < 400), só TCP se o caminho
  estiver vazio, ou "a correr há 5s" num worker. Uma réplica nova que
  reinicie falha o deploy de imediato.
- **Falha antes da troca:** as réplicas novas são removidas e a revisão
  anterior continua a servir, intocada.
- **Routing:** o launcher escreve a configuração do Traefik (file provider)
  só com réplicas da revisão atual que passaram o health check. Uma réplica
  entra depois de estar pronta e sai antes de parar.
- **Host:** cada serviço web tem `<serviço>-<ambiente>-<projeto>.<PUBLIC_BASE_DOMAIN>`,
  mais os domínios próprios ativos.

### A partir do CI

Cria um token na página do projeto (só serve para fazer deploy e ver o
estado dos deploys desse projeto, opcionalmente de um só ambiente) e:

```sh
curl --fail-with-body -X POST \
  -H "Authorization: Bearer $BASE_STACK_DEPLOY_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"image\": \"ghcr.io/eu/loja:$GITHUB_SHA\"}" \
  "https://api.<PUBLIC_BASE_DOMAIN>/v1/projects/loja/environments/production/services/web/deployments?wait=true"
```

`?wait=true` só responde no fim do deploy: 200 se correu bem, 422 se
falhou (com o log no corpo). O Traefik expõe ao exterior **só**
`/v1/projects` desta API.

## Reconciliador

Corre a cada 10 segundos e a pedido (ex.: depois de mudar o scaling):

- repõe réplicas em falta e substitui as paradas;
- faz o health check a todas (três falhas seguidas tiram uma do routing);
- aplica o autoscaling: CPU média relativa ao alocado, uma réplica de cada
  vez, com 90s de cooldown;
- drena as réplicas a mais e limpa as de deploys interrompidos;
- recria containers de addons que desapareçam (o volume de dados fica);
- volta a verificar domínios pendentes;
- corre os backups diários dos addons Postgres (guarda os 7 mais recentes).

Quando não consegue repor um serviço (ex.: quota esgotada), o motivo
aparece na UI.

## API

Todas as rotas exigem `Authorization: Bearer <token>`: um access token do
auth com role `infra-admin` ou `super-admin`, ou um token de deploy
(`bsd_...`) nas rotas marcadas com ✱.

| Método e caminho (`/v1/projects/...`) | |
|---|---|
| `GET`, `POST /` · `GET`, `PATCH`, `DELETE /{p}` | projetos (`?purge=true` apaga também os dados) |
| `GET /{p}/usage` | recursos alocados no projeto |
| `POST /{p}/environments` · `DELETE /{p}/environments/{e}` | ambientes |
| `GET`, `PUT`, `DELETE /{p}/registries[/{host}]` | credenciais de registry |
| `GET`, `POST`, `DELETE /{p}/deploy-tokens[/{id}]` | tokens de CI |
| `GET /{p}/environments/{e}/secrets` · `PUT`, `DELETE .../secrets/{nome}` | segredos (só escrita) |
| `GET`, `POST .../addons` · `PATCH`, `DELETE .../addons/{a}` | addons |
| `GET`, `POST .../addons/{a}/backups` · `POST .../addons/{a}/restore` | backups (Postgres) |
| `GET`, `POST .../services` · `GET`✱, `PATCH`, `DELETE .../services/{s}` | serviços |
| `POST .../services/{s}/start`, `/stop` | ligar/desligar sem perder nada |
| `GET`✱, `POST`✱ `.../services/{s}/deployments` · `GET`✱ `.../deployments/{id}` | deploys e rollback (`{"revision": n}`) |
| `GET .../services/{s}/revisions`, `/replicas` | histórico e estado ao vivo |
| `POST .../services/{s}/run` | comando pontual com a revisão atual |
| `GET`, `POST .../services/{s}/domains` · `POST .../domains/{h}/verify` · `DELETE .../domains/{h}` | domínios próprios |
| `GET .../environments/{e}/jobs[/{id}]` | jobs e o seu output |

## Configuração

| Variável | |
|---|---|
| `LAUNCHER_DATABASE_URL` | Postgres da plataforma (schema `launcher`) |
| `LAUNCHER_SECRETS_KEY` | 32 bytes em base64 (`openssl rand -base64 32`); sem ela não há segredos nem addons |
| `LAUNCHER_TRAEFIK_DYNAMIC_DIR` | onde escrever o routing (volume partilhado com o Traefik); vazio desliga |
| `PUBLIC_BASE_DOMAIN` | domínio dos hosts por defeito e da API (`api.<domínio>`) |
| `PUBLIC_TLS_CERT_RESOLVER` | ex.: `le` liga HTTPS e o redirect de HTTP |
| `LAUNCHER_PUBLIC_IPS` | IPs públicos do host, para verificar o DNS dos domínios próprios |
| `LAUNCHER_ADDON_POSTGRES_IMAGE`, `LAUNCHER_ADDON_REDIS_IMAGE` | por defeito `postgres:17-alpine`, `redis:7-alpine` |
| `LAUNCHER_HOST_MEMORY_MB`, `LAUNCHER_MEMORY_OVERCOMMIT_RATIO` | teto de memória do host |
| `LAUNCHER_SELF_CONTAINER`, `TRAEFIK_CONTAINER` | containers ligados a todas as redes de ambiente |

## Limitações conhecidas

- **Um só host.** Escalar significa mais réplicas na mesma máquina.
- **Serviços sem volumes.** O estado vive nos addons. Uploads devem ir para
  object storage.
- **Uma base por addon Postgres.** Para outra base, cria outro addon (que é
  também outro cluster, com backups próprios).
- **Redis** persiste em AOF no volume, mas não tem backups agendados.
- **Quotas e deploys:** durante um deploy as réplicas novas e antigas
  coexistem por instantes, e a quota tem de ter margem para isso.
- **Groups do fluxo antigo:** continuam com o socket do Docker direto.
