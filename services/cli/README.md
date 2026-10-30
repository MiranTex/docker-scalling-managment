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
| `basestack set <chave> <valor>` | muda `domain`, `portal-port`, `admin-email`, `acme-email` ou `images-from` (os segredos ficam); aplica com `up` |
| `basestack pull` | busca as imagens da plataforma a `images-from` sem as aplicar (o `up` também as busca, sempre) |
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

## Noutra máquina (sem o código)

Os binários para Mac e Linux (Intel/AMD e ARM) saem de `make release`
(`bin/release/basestack-<os>-<arch>`, idem `bs`). Numa máquina sem o
repositório, as imagens da plataforma vêm do registry de uma instalação que
as construiu -- com as duas arquiteturas, para servirem em qualquer uma:

```sh
# na máquina com o código
basestack dev build --platform linux/amd64,linux/arm64 --push

# na máquina nova (confia antes na CA dessa instalação -- ver abaixo)
docker login registry.<domínio da outra>            # email + API key de um admin da plataforma
basestack init --images-from registry.<domínio da outra>/basestack
basestack up
```

Várias arquiteturas numa imagem precisam do image store do containerd
(padrão no Docker Engine 29+; no Docker Desktop: Settings > General >
"Use containerd for pulling and storing images"). As imagens de Go
compilam já para o alvo, sem emulação; o portal (Node) corre emulado e é o
mais lento. O mesmo serve para as apps:
`docker build --platform linux/amd64,linux/arm64 -t registry.<domínio>/<projeto>/app:1.0 . && docker push ...`.

## Nós e modo cluster

Outras máquinas Linux entram como **nós**: no portal, Nós > Adicionar nó
(ou `basestack node token` na máquina da plataforma) dá o comando a
correr no nó novo:

```sh
basestack node join https://api.<domínio> --token bsj_... --ca sha256:...
```

O join confere a CA da plataforma pela impressão digital, põe o Docker do
nó a confiar no registry (pede `sudo`) e arranca o agente
(`basestack-agent`), que se liga à plataforma -- o nó não abre portas.
`basestack node status|start|leave` gerem-no depois.

Para os nós receberem apps, a plataforma corre em **modo cluster** (só
Linux): `basestack init --cluster`, ou `basestack set cluster on && basestack up`.
A máquina da plataforma passa a manager de um swarm (só para as redes:
quem decide onde corre cada app continua a ser o launcher), cada nó entra
sozinho, e a rede de cada ambiente passa a overlay cifrada -- as apps de
um nó chegam aos addons e o Traefik chega a elas como se estivessem todas
na mesma máquina. As redes dos ambientes que já existiam são migradas no
`up` (um corte curto). Entre as máquinas têm de estar abertas as portas
2377/tcp (só a da plataforma), 7946/tcp+udp, 4789/udp e o protocolo ESP
(a overlay é cifrada com IPsec). Com a ufw ativa, o `up` (em modo
cluster) e o `node join` oferecem-se para as abrir, só para a rede local
da máquina; `basestack firewall` faz o mesmo a qualquer altura. Outra
firewall (a do router, firewalld): o `basestack firewall` diz o que abrir.

Os addons ficam sempre na máquina da plataforma (os dados estão lá).

**Quando um nó falha.** A plataforma só vê um nó pelo túnel do agente, e
túnel em baixo não quer dizer réplicas perdidas:

- até 60s sem sinal, as réplicas dele continuam a contar (nada se cria
  noutro lado); as que não respondem ao health check (feito pela overlay,
  que não depende do túnel) saem logo do routing, e um pedido que ainda
  lá caia é repetido noutra réplica;
- passados 60s o nó fica **perdido** e as réplicas dele renascem noutros
  nós; se ele voltar, as que lá ficaram são as mais antigas e saem;
- depois de reiniciar, a plataforma espera pelos nós (até 60s) antes de
  mexer nas réplicas -- sem saber o que lá corre, criava duplicados.

**Esvaziar** (portal, Nós) põe um nó em manutenção e muda as réplicas dele
para outros, uma a uma, como num deploy -- para reiniciar ou atualizar a
máquina sem cortar o serviço.

Se cair a máquina da plataforma, cai o portal, a API, o Traefik e os
addons: as apps nos nós continuam a correr, mas sem tráfego de fora. Isso
é alta disponibilidade, uma fase à parte.

### Cluster de bolso (desenvolvimento)

`scripts/cluster-dind.sh` monta uma plataforma em modo cluster e dois nós,
cada um num Docker-in-Docker, no Docker desta máquina (serve o Docker
Desktop do Mac):

```sh
make release && basestack dev build      # binários e imagens
scripts/cluster-dind.sh up               # plataforma + 2 nós ligados
scripts/cluster-dind.sh test             # deploy nos nós, Traefik e Postgres pela overlay
scripts/cluster-dind.sh reload launcher  # depois de reconstruir uma imagem
scripts/cluster-dind.sh down
```

## Registry de imagens

`basestack up` arranca também um registry em `registry.<domínio>` (HTTPS
pelo Traefik). As imagens de um projeto vivem em
`registry.<domínio>/<projeto>/<nome>:<tag>`:

```sh
docker login registry.<domínio>        # utilizador: o teu email; password: uma API key (ak_..., scope platform)
docker tag app:1.0 registry.<domínio>/loja/app:1.0
docker push registry.<domínio>/loja/app:1.0
```

Quem pode o quê segue o papel no projeto: viewer busca, developer e admin
também enviam; no CI, um token de deploy (`bsd_...`) como password envia e
busca as do seu projeto. Os deploys buscam de lá sozinhos (credencial
interna do launcher), mas só imagens do próprio projeto.
`basestack dev build --push` envia as imagens da plataforma
(`registry.<domínio>/basestack/...`).

**HTTPS com a CA da instalação.** Sem domínio público, o certificado do
registry é assinado por uma CA própria (`~/.basestack/tls/ca.crt`), e cada
máquina que usa o registry tem de confiar nela:

- no Mac, o `basestack up` põe-na em `~/.docker/certs.d/` --
  **reinicia o Docker Desktop uma vez**;
- no Linux, o `basestack up` mostra os comandos (abaixo) e, num terminal,
  oferece-se para os correr com `sudo` antes de arrancar a plataforma. Com
  Docker 29+ (image store do containerd) o pedido do token só confia nas
  CAs do sistema -- o `certs.d` não chega;
- noutra máquina Linux que use este registry, o mesmo à mão, com o
  `ca.crt` copiado desta:
  ```sh
  sudo mkdir -p /etc/docker/certs.d/registry.<domínio>
  sudo cp ca.crt /etc/docker/certs.d/registry.<domínio>/ca.crt
  sudo cp ca.crt /usr/local/share/ca-certificates/basestack-registry.<domínio>.crt
  sudo update-ca-certificates && sudo systemctl restart docker
  ```

Com `acme-email` (domínio público), o Let's Encrypt serve o registry e nada
disto é preciso.

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
