// cmd/cli é a camada de linha de comando do autoscaler: comandos avulsos de
// operação/inspeção, em vez de subir um processo de longa duração como
// cmd/group. Cada comando vive em seu próprio arquivo neste pacote.
package main

import (
	"fmt"
	"os"
)

// commands mapeia o nome do subcomando pra sua implementação. Adicionar um
// comando novo é só acrescentar uma entrada aqui.
var commands = map[string]func(args []string){
	"list": runList,
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	name := os.Args[1]
	if name == "help" || name == "-h" || name == "--help" {
		printUsage()
		return
	}

	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\n\n", name)
		printUsage()
		os.Exit(1)
	}

	cmd(os.Args[2:])
}

func printUsage() {
	fmt.Println(`autoscaler-cli: ferramenta de linha de comando do autoscaler

Uso:
  autoscaler-cli <comando> [flags]

Comandos:
  list    lista os containers do host com as informações necessárias para
          colocá-los sob monitorização, autoscaling e balancing`)
}
