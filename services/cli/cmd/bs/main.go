// bs é a CLI da plataforma base-stack: um terminal num serviço ou addon
// (bs exec) e túneis até às bases de dados dos projetos (bs port-forward),
// sobre a mesma API do portal. Autentica-se com uma API key pessoal (ak_,
// página "API tokens" do portal) com o scope "platform".
package main

import (
	"errors"
	"fmt"
	"os"
)

const usage = `bs -- CLI da plataforma base-stack

Uso:
  bs login                                  guarda o URL da API e a API key
  bs projects                               lista os teus projetos e ambientes
  bs exec <projeto>/<ambiente>/<nome>       terminal num serviço ou addon
       [--replica <container>] [--debug]
  bs port-forward <projeto>/<ambiente>/<addon>
       [--port <porta local>]               túnel até uma base de dados

A configuração fica em ~/.config/bs/config.json; BS_API_URL e BS_TOKEN
sobrepõem-se a ela (útil em scripts).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "login":
		err = cmdLogin(args)
	case "projects":
		err = cmdProjects(args)
	case "exec":
		err = cmdExec(args)
	case "port-forward", "pf":
		err = cmdPortForward(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("comando desconhecido %q\n\n%s", cmd, usage)
	}
	if err != nil {
		var exit exitCode
		if errors.As(err, &exit) {
			os.Exit(int(exit))
		}
		fmt.Fprintln(os.Stderr, "bs:", err)
		os.Exit(1)
	}
}

// exitCode termina sem mensagem (ex: o exit code da shell remota já disse
// tudo).
type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }
