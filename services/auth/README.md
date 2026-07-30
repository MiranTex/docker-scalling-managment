# auth

Serviço de autenticação do base-stack. Fase 1: registo/login por
email+senha, emissão e rotação de tokens, JWKS. Fase 2 (esta): API keys
para autenticação máquina-a-máquina. Fases futuras: login social via
OAuth2/OIDC, passkeys via WebAuthn.

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

Todo o núcleo criptográfico (JWT, hashing, refresh tokens, API keys) foi
construído com a stdlib do Go, sem biblioteca de terceiros — dá pra ler
`internal/token`, `internal/password`, `internal/refresh` e
`internal/apikey` de ponta a ponta e entender exatamente o que está a
acontecer. As fases futuras (OAuth2/OIDC, WebAuthn) vão trazer bibliotecas
estabelecidas para as partes onde reinventar é mais risco de segurança do
que aprendizagem.

## Endpoints

| Método | Rota | Descrição |
|---|---|---|
| POST | `/v1/register` | `{email, password}` → cria conta |
| POST | `/v1/login` | `{email, password}` → `{access_token, refresh_token, ...}` |
| POST | `/v1/token/refresh` | `{refresh_token}` → novo par de tokens (roda o refresh token) |
| POST | `/v1/logout` | `{refresh_token}` → encerra a sessão |
| POST | `/v1/api-keys` | *(Bearer access token)* `{scopes, ttl_seconds?}` → cria uma API key (`key` só vem nesta resposta) |
| DELETE | `/v1/api-keys/{id}` | *(Bearer access token, precisa ser o dono)* revoga a chave |
| POST | `/v1/api-keys/introspect` | `{key}` → `{active, owner?, scopes?}` — usado por OUTRO serviço para validar uma API key |
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
  opaquetoken/         segredo aleatório + hash, partilhado por refresh e apikey
  idgen/               UUIDs (usado por vários pacotes acima)
  store/               Postgres: users, password_credentials, refresh_tokens, api_keys
  httpapi/             handlers HTTP, ligando tudo o resto
```

Cada pacote depende só de interfaces pequenas dos que usa (`refresh` e
`apikey` recebem uma interface `Store` cada, `httpapi` recebe interfaces
de `UserStore`/`TokenIssuer`/`RefreshIssuer`/`APIKeyIssuer`/`Pinger`) — é
isso que permite testar a lógica de negócio (rotação de token, validação
HTTP) com fakes em memória, e só a camada `store` precisa de Postgres de
verdade nos testes.

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
| `LOG_FORMAT` / `LOG_LEVEL` | `json` / `info` | Mesmo padrão do services/autoscaler. |

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

Todos os outros pacotes (`password`, `token`, `refresh`, `idgen`,
`keystore`, `httpapi`) são testados com fakes/em memória — cobrem
inclusive os cenários de ataque mais importantes: assinatura adulterada,
token expirado, chave errada, e reuso de refresh token (que precisa
revogar a sessão inteira, não só o token usado).

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
- Sem listagem ("minhas chaves") nem rotação de API keys — só
  criar/revogar. O índice por `owner` na tabela já existe para quando
  isso for adicionado.
