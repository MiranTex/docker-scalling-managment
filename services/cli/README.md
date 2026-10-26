# CLIs da plataforma

Dois binários do mesmo código, para públicos diferentes:

- **`basestack`** -- para quem **opera** a plataforma: instala, arranca,
  pára e mostra o estado (precisa do Docker desta máquina).
- **`bs`** -- para quem **usa** os projetos: terminal e túneis, pela API
  (precisa só de rede e de uma API key).

## basestack

```sh
make install-basestack      # -> /usr/local/bin/basestack
basestack doctor            # Docker, memória, portas
basestack dev build         # constrói as imagens a partir do código (dentro do repositório)
basestack init              # domínio, porta do portal, email do admin; gera os segredos
basestack up                # arranca tudo: plataforma + métricas e logs
```

Na primeira vez: regista-te no portal com o email do admin e corre
`basestack restart auth` -- essa conta passa a super-admin.

| Comando | |
|---|---|
| `basestack status` | estado de cada componente e os endereços |
| `basestack logs <componente> [-f]` | ex.: `launcher`, `auth`, `portal`, `grafana` |
| `basestack restart [componente]` | |
| `basestack set <chave> <valor>` | muda `domain`, `portal-port`, `admin-email` ou `acme-email` (os segredos ficam); aplica com `up` |
| `basestack up` | também aplica mudanças feitas em `settings.json` |
| `basestack down` | pára a plataforma; os dados ficam e as apps dos projetos continuam a correr |
| `basestack down --all` | pára também as apps e bases de dados dos projetos (os dados ficam); no `up` seguinte o launcher volta a ligá-las |
| `basestack uninstall` | apaga a plataforma e os dados dela (pede confirmação) |

O portal fica em `http://portal.<domínio>` (pelo Traefik, como a API em
`api.<domínio>`), por isso funciona de qualquer máquina que resolva o
domínio; `http://localhost:<porta do portal>` continua a servir nesta
máquina. Por defeito o `init` propõe `<ip desta máquina>.nip.io`, que toda
a rede local resolve -- precisa de acesso à internet (o nip.io é um DNS
público) e de um IP fixo (reserva-o no router). Se o IP mudar:
`basestack set domain <ip novo>.nip.io && basestack up`.

Tudo vive em `~/.basestack` (Linux como root: `/var/lib/basestack`;
`BASESTACK_HOME` muda-o): `settings.json` com as escolhas e os
**segredos gerados** (guarda uma cópia -- sem ele os dados cifrados não se
recuperam), e o `compose.yml` + `config/` gerados a cada `up`. A definição
da stack vai embutida no binário (`cmd/basestack/assets`); as configurações
de observabilidade são cópias de `services/monitoring`, e um teste falha se
se afastarem.

Tudo o que é da plataforma tem nomes próprios (`basestack-*`), por isso
nunca partilha redes, volumes ou chaves com a demo antiga
(`demo/docker-compose.yml`) -- mas usam as mesmas portas: só uma de cada
vez (`basestack up` recusa se as portas estiverem ocupadas).

# bs

Terminal nos serviços e addons dos teus projetos, e túneis até às bases de
dados, a partir da tua máquina. Fala com a mesma API do portal (o launcher,
em `api.<domínio>`), com o teu acesso: só vês os projetos de que és membro,
e `exec`/`port-forward` exigem o papel developer ou admin.

## Instalar

```sh
make install-cli    # na raiz do repositório -> /usr/local/bin/bs (pede sudo se preciso)
make cli            # só compila, para bin/bs
```

Para atualizar depois de mudanças, corre `make install-cli` outra vez.

## Entrar

Cria uma API key no portal ("API tokens") com o scope **platform** e:

```sh
bs login
# URL da API [http://api.127.0.0.1.nip.io]:
# API key (ak_..., com o scope "platform"):
```

Fica guardada em `~/.config/bs/config.json` (só legível por ti). Em
scripts, `BS_API_URL` e `BS_TOKEN` sobrepõem-se ao ficheiro.

## Comandos

```sh
bs projects                                   # projetos, ambientes, serviços e addons
bs exec gymapp/dev/web                        # shell numa réplica do serviço
bs exec gymapp/dev/web --replica <container>  # numa réplica específica
bs exec gymapp/dev/web --debug                # busybox ao lado (imagens sem shell)
bs exec gymapp/dev/gymapp-database            # shell no addon (psql -U app -d app)
bs port-forward gymapp/dev/gymapp-database    # túnel em 127.0.0.1:5432
bs port-forward gymapp/dev/cache --port 16379 # outra porta local
```

`port-forward` mostra a ligação pronta a usar -- o URL, o JDBC e os campos
um a um (com a password do addon -- fica registado na auditoria do
launcher):

```
A encaminhar 127.0.0.1:5432 -> gymapp/dev/gymapp-database

  URL:        postgres://app:...@127.0.0.1:5432/app?sslmode=disable
  JDBC:       jdbc:postgresql://127.0.0.1:5432/app  (DBeaver, DataGrip)
  Host:       127.0.0.1
  Porta:      5432
  Base dados: app
  Utilizador: app
  Password:   ...

Deixa isto a correr enquanto usas o cliente. Ctrl+C para fechar.
```

No DBeaver: Nova ligação → PostgreSQL → "Connect by: Host" com os campos,
ou "Connect by: URL" com o JDBC (utilizador e password nos campos deles).

O túnel só escuta em `127.0.0.1`. Cada ligação do cliente abre o seu
próprio túnel (máx. 20 por conta), que fecha após 30 min sem tráfego.
