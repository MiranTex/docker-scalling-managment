# bs -- CLI da plataforma

Terminal nos serviços e addons dos teus projetos, e túneis até às bases de
dados, a partir da tua máquina. Fala com a mesma API do portal (o launcher,
em `api.<domínio>`), com o teu acesso: só vês os projetos de que és membro,
e `exec`/`port-forward` exigem o papel developer ou admin.

## Instalar

```sh
make cli            # na raiz do repositório -> bin/bs
```

Copia `bin/bs` para um sítio no `PATH` (ex: `/usr/local/bin`).

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

`port-forward` mostra a ligação pronta a usar (com a password do addon --
fica registado na auditoria do launcher):

```
A encaminhar 127.0.0.1:5432 -> gymapp/dev/gymapp-database
  postgres://app:...@127.0.0.1:5432/app?sslmode=disable
Ctrl+C para fechar.
```

O túnel só escuta em `127.0.0.1`. Cada ligação do cliente abre o seu
próprio túnel (máx. 20 por conta), que fecha após 30 min sem tráfego.
