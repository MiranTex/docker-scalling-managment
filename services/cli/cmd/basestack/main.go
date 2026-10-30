// basestack instala e gere a plataforma base-stack numa máquina: gera a
// configuração e os segredos (init), arranca e pára a stack inteira --
// plataforma e observabilidade -- e mostra o estado. É para quem opera a
// plataforma; quem só usa os projetos tem o bs (cmd/bs).
//
// A definição da stack vai embutida neste binário (assets/): quem instala
// não precisa do repositório, só do Docker e das imagens.
package main

import (
	"fmt"
	"os"
)

// version vem do build (-ldflags "-X main.version=..."): o commit do git.
var version = "dev"

const usage = `basestack -- instala e gere a plataforma base-stack nesta máquina

Uso:
  basestack init        configura (domínio, portas, admin) e gera os segredos
  basestack up          arranca a plataforma (ou aplica a configuração nova)
  basestack status      estado de cada componente e endereços
  basestack logs <componente> [-f]
  basestack restart [componente]
  basestack set <chave> <valor>   muda a configuração (domain, portal-port,
                        admin-email, acme-email, images-from, cluster on|off);
                        aplica-se no próximo up
  basestack pull        volta a buscar as imagens da plataforma (images-from)
  basestack down        pára a plataforma (as apps dos projetos continuam)
  basestack down --all  pára a plataforma e as apps e bases de dados dos projetos
  basestack doctor      verifica os requisitos desta máquina
  basestack reset-password <email>
                        password temporária para uma conta (ex: o admin esqueceu a dele)
  basestack reset-2fa <email>
                        desliga o 2FA de uma conta (perdeu o telemóvel e os códigos)
  basestack firewall    abre na ufw a rede do cluster (só para a rede local)
  basestack dev build   constrói as imagens a partir do código (desenvolvimento)
  basestack dev build --platform linux/amd64,linux/arm64 --push
                        constrói para várias arquiteturas e envia para o registry
  basestack dev build --push-to registry.<domínio de outra plataforma>
                        envia para o registry dessa (com docker login lá)
  basestack uninstall   remove a plataforma e os dados dela
  basestack version     a versão deste binário e das imagens da plataforma
  basestack upgrade     atualiza este binário (e, num nó, o agente) a partir do registry
  basestack trust <https://api.domínio>
                        o docker desta máquina passa a confiar no registry de
                        outra plataforma (ex: para lhe enviar imagens)

Nós (esta máquina a correr apps de uma plataforma noutra):
  basestack node join <url> --token bsj_... --ca sha256:...
                        o comando vem do portal (Nós > Adicionar nó)
  basestack node status | start | leave
  basestack node set-api https://api.<domínio>
                        a plataforma mudou de endereço: o nó segue-a
  basestack node token  (na máquina da plataforma) um token de entrada e o comando

Os dados e a configuração ficam em ~/.basestack (Linux como root:
/var/lib/basestack); BASESTACK_HOME muda o sítio.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "init":
		err = cmdInit(args)
	case "up":
		err = cmdUp(args)
	case "status", "ps":
		err = cmdStatus(args)
	case "logs":
		err = cmdLogs(args)
	case "restart":
		err = cmdRestart(args)
	case "set":
		err = cmdSet(args)
	case "down", "stop":
		err = cmdDown(args)
	case "doctor":
		err = cmdDoctor(args)
	case "reset-2fa":
		err = cmdReset2FA(args)
	case "reset-password":
		err = cmdResetPassword(args)
	case "firewall":
		err = cmdFirewall(args)
	case "node":
		err = cmdNode(args)
	case "pull":
		err = cmdPull(args)
	case "dev":
		err = cmdDev(args)
	case "uninstall":
		err = cmdUninstall(args)
	case "version", "--version":
		err = cmdVersion(args)
	case "upgrade":
		err = cmdUpgrade(args)
	case "trust":
		err = cmdTrust(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("comando desconhecido %q\n\n%s", cmd, usage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "basestack:", err)
		os.Exit(1)
	}
}
