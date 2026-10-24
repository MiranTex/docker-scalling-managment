# bs -- CLI da plataforma

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
