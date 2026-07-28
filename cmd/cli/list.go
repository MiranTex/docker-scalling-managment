package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"autoscaler/internal/dockerclient"
	"github.com/joho/godotenv"
)

// serviceLabel é a label que o resto do sistema (scaler, executor,
// loadbalancer) usa pra saber que um container está sob autoscaling. O
// comando list mostra o valor atual dela pra cada container, pra ficar claro
// quem já está "adotado" e quem ainda não tem essa label.
const serviceLabel = "autoscaler.service"

func runList(args []string) {

	_ = godotenv.Load()


	fs := flag.NewFlagSet("list", flag.ExitOnError)
	all := fs.Bool("all", false, "incluir containers parados, não só os em execução")
	socket := fs.String("socket", os.Getenv("HOME")+os.Getenv("DOCKER_SOCKET"), "caminho do unix socket do Docker")
	fs.Parse(args)

	ctx := context.Background()
	client := dockerclient.New(*socket)

	containers, err := client.ListContainers(ctx, *all, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "erro listando containers: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNOME\tIMAGEM\tESTADO\tIP\tLABEL_KEY\tLABEL_VALUE")
	for _, ctr := range containers {
		ip := "-"
		// Só faz sentido resolver IP de container em execução: um container
		// parado não tem endereço de rede atribuído.
		if ctr.State == "running" {
			if inspect, err := client.InspectContainer(ctx, ctr.ID); err == nil {
				if addr := inspect.IPAddress(); addr != "" {
					ip = addr
				}
			}
		}

		// LABEL_KEY é sempre a mesma (é a constante que o resto do sistema
		// usa pra reconhecer um serviço); LABEL_VALUE é o que de fato
		// diferencia a qual grupo/serviço este container pertence.
		labelValue := "-"
		if v, ok := ctr.Labels[serviceLabel]; ok {
			labelValue = v
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ctr.ID[:12], containerName(ctr), ctr.Image, ctr.State, ip, serviceLabel, labelValue)
	}
	w.Flush()
}

// containerName devolve o primeiro nome do container sem a barra inicial que
// a API do Docker sempre inclui (ex: "/demo-1" -> "demo-1").
func containerName(ctr dockerclient.Container) string {
	if len(ctr.Names) == 0 {
		return "-"
	}
	return strings.TrimPrefix(ctr.Names[0], "/")
}
