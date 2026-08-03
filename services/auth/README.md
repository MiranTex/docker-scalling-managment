# auth

Serviço de autenticação do base-stack. Fase 1: registo/login por
email+senha, emissão e rotação de tokens, JWKS. Fase 2: API keys para
autenticação máquina-a-máquina. Fase 3 (esta): login social via
OAuth2/OIDC (Google, GitHub). Fase futura: passkeys via WebAuthn.

Nota de escopo (relevante se no futuro este serviço também for atuar como
*authorization server* para apps de terceiros pedirem autorização a um
utilizador nosso): o que está aqui é o inverso disso -- nós somos o
*client* OAuth, autenticando os nossos próprios utilizadores contra um
provider externo. Ser authorization server é uma feature bem maior e
diferente (registo de clients de terceiros, ecrã de consentimento, scopes
por app externa) e ficaria numa fase própria, construída em cima do que já
existe aqui (reaproveitando login, JWT, Postgres) sem precisar alterar o
que já está pronto.

## Como funciona

- **Access token = JWT RS256**, curto (15 min por default). Autocontido:
  qualquer serviço valida com a chave pública, sem falar com o auth
  service em tempo real.
- **Refresh token = opaco** (não é JWT), longo (14 dias por default),
  guardado só como hash. **Rotação a cada uso**: usar um refresh token o
  invalida e emite um novo. Se um token já usado for reapresentado (sinal
  de roubo), a sessão inteira é revogada — não só o token comprometido.
- **JWKS em `/.well-known/jwks.json`**: expõe a(s) chave(s) pública(s)
  ativa(s). É o que permite qualquer projeto (Go, Laravel, o que for)
  validar tokens deste serviço sem partilhar segredo nenhum.
- **Senhas com Argon2id** (KDF recomendada pela OWASP hoje), salt único
  por senha, comparação em tempo constante.
- Login devolve **a mesma mensagem de erro** para "e-mail não existe" e
  "senha errada" — de propósito, para não permitir enumerar e-mails
  registados.
- **API keys** (`ak_...`) para autenticação máquina-a-máquina: longa
  duração (ou sem expiração), criadas por um utilizador autenticado,
  validadas por qualquer outro serviço via `/v1/api-keys/introspect` — o
  equivalente, para chamadas M2M, ao que a JWKS é para sessões de
  utilizador.
- **Login social (Google/GitHub)**: o utilizador autentica-se no provider
  externo, e este serviço liga essa identidade à conta com o mesmo e-mail
  (ou cria uma nova, se não existir). A ligação a uma conta **já
  existente** só acontece se o provider confirmar que o e-mail é
  verificado -- senão, qualquer um poderia reivindicar um e-mail que não é
  dele e assumir a conta de outra pessoa. Uma mesma conta pode ter senha
  + Google + GitHub ao mesmo tempo (account linking).
- **Verificação de e-mail**: uma conta criada por senha nasce com
  `email_verified_at` nulo -- ninguém provou ainda que é dona daquele
  e-mail. `POST /v1/register` emite um token de verificação de uso único
  (`internal/verification`); `POST /v1/verify-email` confirma. O login
  por senha **não** exige verificação prévia (não bloqueia o uso
  imediato da conta), mas o login social exige, indiretamente: ver a
  próxima secção sobre o porquê disto ser mais que uma formalidade.

### A vulnerabilidade que a verificação de e-mail fecha

Sem verificação de e-mail, o account linking do login social tinha um
problema real: um atacante regista `vitima@empresa.com` por senha (o
registo aceita qualquer e-mail, sem prova nenhuma de posse). Meses depois,
a vítima de verdade tenta "Entrar com Google" com o seu e-mail real
(verificado pelo Google) -- o sistema encontra a conta já existente
(criada pelo atacante) e, como o Google confirma o e-mail, **ligava** a
identidade Google a essa conta. O atacante continuava a saber a senha:
acesso total a uma conta que a vítima acha que é dela.

A correção (`store.ReclaimUnverifiedAccount`, chamada por
`oauth.Manager.Login` sempre que a conta encontrada por e-mail nunca foi
verificada do nosso lado): em vez de só ligar a identidade cegamente,
tratamos o login social como prova de posse legítima e **reclamamos** a
conta -- marcamos o e-mail como verificado, removemos a credencial de
senha que já existisse (o atacante perde o acesso na hora) e revogamos
todas as sessões (refresh tokens) ativas. Limitação aceite: um access
token (JWT) já emitido para essa conta antes disto continua válido até
expirar (15 min por default) -- JWT não é revogável antes do tempo, só o
refresh token é.

Todo o núcleo criptográfico (JWT, hashing, refresh tokens, API keys) foi
construído com a stdlib do Go, sem biblioteca de terceiros — dá pra ler
`internal/token`, `internal/password`, `internal/refresh` e
`internal/apikey` de ponta a ponta e entender exatamente o que está a
acontecer. Login social é a exceção deliberada: usa
`golang.org/x/oauth2` (mantido pela equipa do Go) para o protocolo OAuth2
em si -- aqui reinventar não tem ganho educativo que compense o risco de
um erro sutil no fluxo. A fase futura (WebAuthn) vai seguir o mesmo
raciocínio.

## Endpoints

| Método | Rota | Descrição |
|---|---|---|
| POST | `/v1/register` | `{email, password}` → cria conta |
| POST | `/v1/login` | `{email, password}` → `{access_token, refresh_token, ...}` |
| POST | `/v1/token/refresh` | `{refresh_token}` → novo par de tokens (roda o refresh token) |
| POST | `/v1/logout` | `{refresh_token}` → encerra a sessão |
| POST | `/v1/verify-email` | `{token}` → confirma o e-mail da conta |
| POST | `/v1/verify-email/resend` | `{email}` → reemite o token (sempre 202, exista ou não a conta) |
| POST | `/v1/api-keys` | *(Bearer access token)* `{scopes, ttl_seconds?}` → cria uma API key (`key` só vem nesta resposta) |
| GET | `/v1/api-keys` | *(Bearer access token)* lista as chaves do utilizador autenticado (nunca inclui o segredo) |
| DELETE | `/v1/api-keys/{id}` | *(Bearer access token, precisa ser o dono)* revoga a chave |
| POST | `/v1/api-keys/introspect` | `{key}` → `{active, owner?, scopes?}` — usado por OUTRO serviço para validar uma API key |
| GET | `/v1/oauth/{provider}/start` | Redireciona (302) para o provider (`google` ou `github`) |
| GET | `/v1/oauth/{provider}/callback` | Recebido do provider após login → `{access_token, refresh_token, ...}` |
| GET | `/.well-known/jwks.json` | Chaves públicas para validar tokens |
| GET | `/healthz` | Liveness |
| GET | `/readyz` | Readiness (confere se a base de dados está alcançável) |

## Estrutura

```
cmd/authd/            binário (config, logging, wiring)
internal/
  password/           hash/verificação de senha (Argon2id)
  token/               emissão/validação de JWT RS256 + JWKS
  keystore/            persiste a chave de assinatura entre restarts
  refresh/             refresh tokens: rotação + deteção de reuso
  apikey/              API keys: emissão/validação/revogação (M2M)
  oauth/               login social: providers (Google/GitHub) + account linking
  verification/        verificação de e-mail: emissão/confirmação de token de uso único
  opaquetoken/         segredo aleatório + hash, partilhado por refresh, apikey e verification
  idgen/               UUIDs (usado por vários pacotes acima)
  store/               Postgres: users, password_credentials, refresh_tokens, api_keys, oauth_identities, email_verifications
  httpapi/             handlers HTTP, ligando tudo o resto
```

Cada pacote depende só de interfaces pequenas dos que usa (`refresh`,
`apikey`, `oauth` e `verification` recebem uma interface `Store` cada,
`httpapi` recebe interfaces de
`UserStore`/`TokenIssuer`/`RefreshIssuer`/`APIKeyIssuer`/`OAuthLogin`/`EmailVerifier`/`Pinger`)
— é isso que permite testar a lógica de negócio (rotação de token,
validação HTTP, account linking) com fakes em memória, e só a camada
`store` precisa de Postgres de verdade nos testes.

## Configuração (variáveis de ambiente)

| Variável | Default | Descrição |
|---|---|---|
| `AUTH_DATABASE_URL` | *(obrigatória)* | DSN do Postgres. |
| `AUTH_LISTEN_ADDR` | `:8080` | Endereço do servidor HTTP. |
| `AUTH_SIGNING_KEY_FILE` | `/var/lib/auth/signing-key.json` | Onde persistir a chave RSA (precisa de volume). |
| `AUTH_ISSUER` | `auth-service` | Claim `iss` emitido/validado nos JWT. |
| `AUTH_AUDIENCE` | `base-stack` | Claim `aud` emitido/validado nos JWT. |
| `AUTH_ACCESS_TOKEN_TTL_SECONDS` | `900` (15 min) | Validade do access token. |
| `AUTH_REFRESH_TOKEN_TTL_SECONDS` | `1209600` (14 dias) | Validade do refresh token. |
| `AUTH_EMAIL_VERIFICATION_TTL_SECONDS` | `86400` (24h) | Validade do token de verificação de e-mail. |
| `LOG_FORMAT` / `LOG_LEVEL` | `json` / `info` | Mesmo padrão do services/autoscaler. |
| `AUTH_GOOGLE_CLIENT_ID` / `AUTH_GOOGLE_CLIENT_SECRET` / `AUTH_GOOGLE_REDIRECT_URL` | *(vazio = desabilitado)* | Habilita login via Google. As três precisam estar definidas. |
| `AUTH_GITHUB_CLIENT_ID` / `AUTH_GITHUB_CLIENT_SECRET` / `AUTH_GITHUB_REDIRECT_URL` | *(vazio = desabilitado)* | Habilita login via GitHub. As três precisam estar definidas. Scope necessário: `user:email`. |

Sem nenhum provider configurado, o serviço sobe normalmente -- os
endpoints `/v1/oauth/*` só respondem "provider desconhecido" (404). Para
habilitar, registe uma aplicação OAuth no [Google Cloud Console](https://console.cloud.google.com/apis/credentials)
ou nas [OAuth Apps do GitHub](https://github.com/settings/developers),
configurando o redirect URI como
`https://<seu-domínio>/v1/oauth/google/callback` (ou `/github/callback`).

## Subir

```sh
docker build -t auth-service:latest services/auth
docker compose -f services/auth/docker-compose.yml up -d
```

Sobe um Postgres básico dedicado a este serviço (schema `auth`) — quando
o projeto tiver um serviço de base de dados partilhado (um Postgres com
um schema por serviço), isto migra pra lá.

Testar:
```sh
curl -X POST localhost:8081/v1/register -d '{"email":"a@example.test","password":"senha-forte"}'
curl -X POST localhost:8081/v1/login -d '{"email":"a@example.test","password":"senha-forte"}'
curl localhost:8081/.well-known/jwks.json
```

## Testes

```sh
cd services/auth
go test ./...
```

Os testes de `internal/store` (a única camada que fala SQL) são
integração de verdade contra Postgres, gated por
`AUTH_TEST_DATABASE_URL` — sem essa variável definida, são pulados
automaticamente, então `go test ./...` funciona sem exigir infraestrutura.
Para rodá-los:

```sh
docker run -d --name auth-test-pg -e POSTGRES_PASSWORD=test -e POSTGRES_DB=auth_test -p 15432:5432 postgres:16-alpine
export AUTH_TEST_DATABASE_URL="postgres://postgres:test@localhost:15432/auth_test?sslmode=disable"
go test ./...
```

Todos os outros pacotes (`password`, `token`, `refresh`, `apikey`,
`verification`, `idgen`, `keystore`, `httpapi`) são testados com
fakes/em memória — cobrem inclusive os cenários de ataque mais
importantes: assinatura adulterada, token expirado, chave errada, reuso
de refresh token (que precisa revogar a sessão inteira, não só o token
usado), ligação de conta via e-mail não verificado, e a reclamação de
uma conta squatted (`TestLoginReclaimsUnverifiedSquattedAccount` em
`internal/oauth`, e `TestReclaimUnverifiedAccount` em `internal/store`
confirmando contra Postgres real que a credencial de senha é mesmo
removida e as sessões são mesmo revogadas, não só um campo marcado).

`internal/oauth` é testado de um jeito um pouco diferente: sobe um IdP
falso de verdade via `httptest.Server` (não um mock/stub em memória) e
exercita o protocolo OAuth2 real (`golang.org/x/oauth2`) contra ele —
troca de código por token, busca de identidade, tudo via HTTP de verdade,
só que contra um servidor de teste em vez do Google/GitHub reais. Isto NÃO
substitui testar contra o Google/GitHub reais (exigiria credenciais de uma
aplicação OAuth registada em cada um), mas cobre o código deste serviço de
ponta a ponta.

## Limitações conhecidas

- Sem rate limiting / proteção de força bruta em `/v1/login` — fica para
  quando o projeto tiver um API gateway/proxy comum na frente dos
  serviços, ou uma fase dedicada a isso.
- Uma única chave de assinatura ativa por vez — `internal/token.Manager`
  já suporta validar múltiplas chaves (`previous ...Key`) para permitir
  rotação sem invalidar tokens em voo, mas o fluxo operacional de rodar a
  chave (gerar nova, manter a antiga só até expirar) ainda não está
  automatizado.
- Sem `/metrics` (Prometheus) ainda — deliberadamente fora do escopo por
  agora; adicionar depois é de baixo risco (não toca em nada de
  segurança).
- Sem endpoint de "logout de todas as sessões" (revogar todas as famílias
  de refresh token de um utilizador) — hoje só dá pra fazer logout de uma
  sessão de cada vez.
- `POST /v1/api-keys/introspect` **não exige autenticação de quem
  pergunta** — qualquer chamador que alcance o serviço na rede consegue
  verificar se uma API key é válida e descobrir o dono/scopes dela. Isto é
  aceitável só porque este endpoint deve ficar acessível apenas na rede
  interna (os serviços que aceitam API keys como credencial), nunca
  exposto publicamente — o mesmo raciocínio de "rede interna apenas" que
  já vale para `/readyz`. Uma fase futura pode adicionar autenticação de
  serviço-a-serviço aqui também.
- Sem rotação de API keys — só criar/revogar/listar.
- Login social não foi testado contra o Google/GitHub reais (só contra um
  IdP falso via `httptest`, ver seção Testes) — antes de usar em produção,
  valide manualmente o fluxo completo com uma aplicação OAuth real
  registada em cada provider.
- Sem endpoint para "adicionar senha" a uma conta criada originalmente via
  OAuth, nem para desligar um provider já ligado — hoje o login social só
  cria/liga, nunca remove.
- **Sem envio de e-mail de verdade** — `POST /v1/register` emite o token
  de verificação e só o regista no log estruturado do processo
  (`docker logs`), marcado claramente como placeholder. Antes de
  produção, isto precisa de ser ligado a um provedor real (SMTP, SES,
  Postmark, etc.) em `Handler.issueAndLogVerificationToken`
  (`internal/httpapi`).
- Zero logging de eventos de segurança fora da emissão do token de
  verificação — login falhado, criação/revogação de API key, login
  social bem-sucedido, nada disso é registado ainda. Falta pra ter rasto
  de auditoria/deteção de intrusão.
