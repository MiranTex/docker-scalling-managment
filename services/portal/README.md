# portal

Frontend do base-stack: login, controlo de sessão e criação de API tokens
pessoais. É a peça que faltava para um utilizador humano interagir com o
[services/auth](../auth/README.md) sem precisar de `curl` — um serviço
piloto simples que mostra os outros serviços do repositório integrados
("autoscaler, autenticação") sendo consumidos por uma aplicação real.

Não tem base de dados própria nem lógica de negócio: é uma camada fina
(Next.js) que fala com a API do `auth` e guarda a sessão em cookies
`httpOnly` — os tokens (JWT de acesso e refresh token) nunca ficam
acessíveis a JavaScript no browser, então um XSS na página não consegue
roubá-los.

## Como funciona

- **Login/registo**: formulários em `/login` e `/register` que chamam
  `POST /v1/login` e `POST /v1/register` do auth service através de Route
  Handlers próprios (`app/api/auth/*`) — o browser nunca fala direto com o
  auth service, só com o próprio portal.
- **Sessão em cookies `httpOnly`**: no login, o portal guarda
  `access_token` e `refresh_token` como cookies `httpOnly` (mais
  `user_email`, esse sim legível por JS, só para mostrar na UI). Nenhum
  token passa pelo `localStorage`.
- **Renovação automática**: `middleware.ts` protege `/dashboard`,
  `/tokens` e `/api/tokens/*` — se o access token (15 min por default) já
  expirou mas o refresh token ainda vale, o middleware chama
  `POST /v1/token/refresh` e substitui os cookies antes de a página
  carregar. O utilizador não precisa entrar de novo a cada 15 minutos.
- **API tokens pessoais**: `/tokens` lista, cria e revoga API keys do
  utilizador autenticado via `GET/POST /v1/api-keys` e
  `DELETE /v1/api-keys/{id}`. O segredo (`ak_...`) só é mostrado uma vez,
  no momento da criação — exatamente como o auth service se comporta,
  então a UI reforça isso com um aviso explícito.
- **`GET /v1/api-keys`** é um endpoint novo, adicionado no auth service
  junto com este portal (antes só existia criar/revogar) — ver
  `services/auth/internal/apikey`, `internal/store/apikey.go` e
  `internal/httpapi/httpapi.go`.
- **Gestão de utilizadores (`/admin/users`)**: só visível/acessível a
  quem tem a claim `role: super-admin` no access token. Lista todas as
  contas, cria contas novas já com uma role escolhida (`user`, `admin`,
  `infra-admin` ou `super-admin`), e permite trocar a role de uma conta
  existente. A checagem no portal (esconder o link, mostrar "só para
  super-admin" em vez da tabela) é só UX -- a autorização de verdade é
  feita pelo auth service (`requireRole`, `services/auth/internal/httpapi`),
  que recusa qualquer chamada a `/v1/admin/*` cuja role não bata,
  independente do que este ecrã decidir mostrar. Ver
  `services/auth/README.md` para o desenho completo de roles e como
  criar o primeiro super-admin.

## Estrutura

```
app/
  login/, register/            páginas públicas (client components)
  (protected)/dashboard/        sessão ativa: user ID, role, iss, validade do access token
  (protected)/tokens/           listar/criar/revogar API tokens pessoais
  (protected)/admin/users/      gestão de utilizadores (só super-admin) -- criar, listar, trocar role
  api/auth/{login,register,logout}/route.ts   proxy para o auth service + cookies
  api/tokens/[[id]]/route.ts    proxy para /v1/api-keys (list/create/revoke)
  api/admin/users/...           proxy para /v1/admin/users e /v1/admin/users/{id}/role
lib/
  authClient.ts                 cliente HTTP fino para o auth service (server-only)
  session.ts                    set/clear dos cookies de sessão
  jwt.ts                        decode (sem validar) do payload do JWT, só para exibir
middleware.ts                   renova a sessão (refresh token) antes das rotas protegidas
```

## Configuração (variáveis de ambiente)

| Variável | Default | Descrição |
|---|---|---|
| `AUTH_SERVICE_URL` | `http://localhost:8081` | Base URL do auth service. Server-only — nunca é enviada ao browser. |
| `PORT` | `3000` | Porta do servidor Next.js. |

## Subir

Precisa do `services/auth` a correr e alcançável em `AUTH_SERVICE_URL`
(ver [services/auth/README.md](../auth/README.md#subir)).

```sh
docker build -t portal:latest services/portal
docker compose -f services/portal/docker-compose.yml up -d
```

Abre `http://localhost:3100` — mapeado assim (não 3000) porque o Grafana
de `services/monitoring` já usa 3000 (ver `demo/README.md`).

Alternativa: `demo/docker-compose.yml` sobe `auth` + `portal` já ligados
na mesma rede (mais simples que coordenar os dois `docker-compose.yml`
separados) — ver `demo/README.md`.

Local, sem Docker:
```sh
cd services/portal
npm install
cp .env.example .env
npm run dev
```

## Testado manualmente

Fluxo completo validado via `curl` com cookie jar contra o stack real
(Postgres + `authd` + `portal`, todos via Docker, ver
`demo/docker-compose.yml`):
- registo → login → `/dashboard` protegido → criar/listar/revogar token
  → expirar `access_token` e confirmar que o middleware renova via
  `refresh_token` (inclusive que o Route Handler do MESMO pedido já vê o
  token novo, não só o próximo) → logout → `/dashboard` volta a
  redirecionar para `/login`.
- bootstrap do primeiro super-admin (`AUTH_BOOTSTRAP_SUPERADMIN_EMAIL`)
  → login como super-admin → `/admin/users` acessível, criar utilizador
  `infra-admin`, listar, promover a `admin` → confirmado que um
  utilizador comum recebe 403 do auth service ao tentar o mesmo endpoint
  diretamente, e que `/admin/users` no portal mostra "só para
  super-admin" em vez da tabela.

Não foi testado num browser de verdade — antes de dar como pronto para
uso real, abra `/login` num browser e percorra os mesmos fluxos
visualmente.

## Limitações conhecidas

- Sem login social (Google/GitHub) nem verificação de e-mail na UI — o
  auth service já suporta os dois, mas o portal hoje só cobre
  e-mail+senha. Adicionar um botão "Entrar com Google/GitHub" é só ligar
  a `/v1/oauth/{provider}/start`.
- Cookies de sessão sem `maxAge` explícito no refresh token (dura a
  sessão do browser) — o auth service permite sessões de até 14 dias por
  default; para persistir entre reaberturas do browser, adicione
  `maxAge` em `lib/session.ts` coerente com
  `AUTH_REFRESH_TOKEN_TTL_SECONDS`.
- `npm audit` reporta 3 vulnerabilidades "high" herdadas de dependências
  internas do próprio Next.js (`postcss`, `sharp` — build/otimização de
  imagem), não de código deste serviço. Não são exploráveis pelo uso que
  este portal faz (não processamos CSS/imagens de terceiros), mas vale
  revisitar ao atualizar o Next.js.
- Sem testes automatizados (unitários ou e2e) — só validação manual via
  `curl` (ver secção acima). Para um serviço piloto está OK; antes de
  produção, vale adicionar pelo menos testes e2e do fluxo de login/tokens
  (Playwright, por exemplo).
- `/admin/users` não impede um super-admin de despromover a própria
  conta pela UI (o auth service também não bloqueia isso, ver
  `services/auth/README.md`) -- cuidado ao testar com a única conta
  super-admin que existir.
