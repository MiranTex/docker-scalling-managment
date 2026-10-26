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

const usage = `basestack -- instala e gere a plataforma base-stack nesta máquina

Uso:
  basestack init        configura (domínio, portas, admin) e gera os segredos
  basestack up          arranca a plataforma (ou aplica a configuração nova)
  basestack status      estado de cada componente e endereços
  basestack logs <componente> [-f]
  basestack restart [componente]
  basestack set <chave> <valor>   muda a configuração (domain, portal-port,
                        admin-email, acme-email); aplica-se no próximo up
  basestack down        pára a plataforma (as apps dos projetos continuam)
  basestack down --all  pára a plataforma e as apps e bases de dados dos projetos
  basestack doctor      verifica os requisitos desta máquina
  basestack dev build   constrói as imagens a partir do código (desenvolvimento)
  basestack uninstall   remove a plataforma e os dados dela

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
	case "dev":
		err = cmdDev(args)
	case "uninstall":
		err = cmdUninstall(args)
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
