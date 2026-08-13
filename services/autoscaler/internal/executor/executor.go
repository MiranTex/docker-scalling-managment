// Package executor traduz uma scaler.Decision em chamadas reais na API do
// Docker: criar+iniciar um container novo (scale up) ou parar+remover um
// existente (scale down). É a única parte do sistema com permissão de
// efetivamente mudar o estado dos containers.
package executor

import (
	"context"
	"errors"
	"fmt"

	"autoscaler/internal/dockerclient"
	"autoscaler/internal/scaler"
)

// stopTimeoutSeconds é quanto tempo esperamos pelo shutdown gracioso antes
// de matar o container num scale down.
const stopTimeoutSeconds = 10

// Apply executa a decisão do scaler para um serviço. members é o conjunto de
// containers atuais desse serviço (usado como "molde" pra criar réplicas, ou
// como candidatos a remover).
func Apply(ctx context.Context, client *dockerclient.Client, decision scaler.Decision, members []dockerclient.Container) error {
	switch decision.Action {
	case scaler.ScaleUp:
		return scaleUp(ctx, client, members)
	case scaler.ScaleDown:
		return scaleDown(ctx, client, members)
	default:
		return nil
	}
}

// scaleUp usa o primeiro container do grupo como molde: mesma imagem,
// comando, env, labels, volumes montados e rede. É por isso que o container
// novo carrega a mesma label de serviço (passa a contar no grupo na próxima
// avaliação) e consegue enxergar as mesmas dependências (banco, cache) que o
// original.
func scaleUp(ctx context.Context, client *dockerclient.Client, members []dockerclient.Container) error {
	if len(members) == 0 {
		return errors.New("executor: scale up sem containers existentes para usar de molde")
	}
	template := members[0]

	inspect, err := client.InspectContainer(ctx, template.ID)
	if err != nil {
		return fmt.Errorf("executor: inspecionando molde %s: %w", template.ID[:12], err)
	}

	id, err := client.CreateContainer(ctx, "", dockerclient.CreateContainerRequest{
		Image:  inspect.Config.Image,
		Cmd:    inspect.Config.Cmd,
		Env:    inspect.Config.Env,
		Labels: inspect.Config.Labels,
		HostConfig: &dockerclient.CreateHostConfig{
			Binds:       inspect.HostConfig.Binds,
			NetworkMode: inspect.PrimaryNetwork(),
		},
	})
	if err != nil {
		return fmt.Errorf("executor: criando container: %w", err)
	}

	if err := client.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("executor: iniciando container %s: %w", id[:12], err)
	}
	return nil
}

// LaunchTemplate descreve como criar uma réplica nova do zero: imagem,
// comando, env, labels e config de rede/volumes. Ao contrário de scaleUp
// (que clona um container já rodando, escolhido meio arbitrariamente), é
// a partir daqui que cmd/group monta o pedido que manda ao launcher (ver
// internal/launcherclient) para criar toda réplica nova -- inclusive a
// primeira, quando ainda não existe nenhum container do serviço. Também
// significa que mudar o template (ex: nova versão da imagem) só afeta as
// PRÓXIMAS réplicas criadas, não substitui as que já estão rodando.
//
// Criar a partir daqui tem dois caminhos possíveis, escolhidos por
// cmd/group (ver applyScaleUp): se o "env" não referencia nenhum
// ${secret:NOME}, CreateFromTemplate cria localmente, sem precisar do
// launcher -- é o que mantém um group auto-referencial (ex: group-authd,
// cujo AUTH_SERVICE_URL aponta pra ele mesmo) capaz de arrancar a
// PRIMEIRA réplica sozinho, sem depender de um access token que só essa
// réplica poderia emitir. Só quando o "env" tem segredos de verdade é que
// vale a pena depender do launcher (ver
// services/launcher/internal/httpapi, POST /v1/replicas) -- ele é quem
// tem a credencial para o secretsadmin.
type LaunchTemplate struct {
	Image   string            `json:"image"`
	Cmd     []string          `json:"cmd,omitempty"`
	Env     []string          `json:"env,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Binds   []string          `json:"binds,omitempty"`
	Network string            `json:"network,omitempty"`
	// ExtraHosts adiciona entradas estáticas em /etc/hosts de cada réplica,
	// formato "hostname:ip" (ex: "host.docker.internal:host-gateway"). Sem
	// relação com publicação de porta -- deliberadamente fora daqui, já que
	// réplicas do mesmo serviço competiriam pela mesma porta no host; quem
	// expõe o serviço é sempre o proxy do group, nunca porta publicada.
	ExtraHosts []string `json:"extraHosts,omitempty"`
}

// CreateFromTemplate cria+inicia um container diretamente a partir de
// template, sem passar pelo launcher -- só seguro de chamar quando
// template.Env não tem nenhuma referência ${secret:NOME} (quem decide
// isso é cmd/group.applyScaleUp, não este pacote).
func CreateFromTemplate(ctx context.Context, client *dockerclient.Client, template LaunchTemplate) (string, error) {
	if template.Image == "" {
		return "", errors.New("executor: launch template sem \"image\" definida")
	}

	id, err := client.CreateContainer(ctx, "", dockerclient.CreateContainerRequest{
		Image:  template.Image,
		Cmd:    template.Cmd,
		Env:    template.Env,
		Labels: template.Labels,
		HostConfig: &dockerclient.CreateHostConfig{
			Binds:       template.Binds,
			NetworkMode: template.Network,
			ExtraHosts:  template.ExtraHosts,
		},
	})
	if err != nil {
		return "", fmt.Errorf("executor: criando container a partir do launch template: %w", err)
	}

	if err := client.StartContainer(ctx, id); err != nil {
		return "", fmt.Errorf("executor: iniciando container %s: %w", id[:12], err)
	}
	return id, nil
}

// scaleDown escolhe e remove imediatamente um container do grupo. Usado
// pelo fluxo simples (sem drain do load balancer); cmd/group usa
// SelectScaleDownTarget + Remove separadamente para tirar o alvo dos
// backends antes de pará-lo.
func scaleDown(ctx context.Context, client *dockerclient.Client, members []dockerclient.Container) error {
	target, err := SelectScaleDownTarget(ctx, client, members)
	if err != nil {
		return err
	}
	return Remove(ctx, client, target.ID)
}

// SelectScaleDownTarget escolhe qual container seria removido num scale
// down, sem removê-lo: consulta o uso de CPU atual de cada candidato e
// escolhe o menos carregado, reduzindo a chance de derrubar um container
// que está no meio de requisições pesadas (o antigo critério, "o último da
// lista", não tinha relação nenhuma com carga real). Separado de Remove
// para permitir tirar o alvo dos backends do load balancer antes de
// efetivamente pará-lo.
//
// Se a coleta de stats falhar para algum candidato, ele é ignorado (tratado
// como "carga desconhecida", nunca preferido sobre um candidato com dado
// real); se falhar para todos, cai de volta a remover o último da lista.
func SelectScaleDownTarget(ctx context.Context, client *dockerclient.Client, members []dockerclient.Container) (dockerclient.Container, error) {
	if len(members) == 0 {
		return dockerclient.Container{}, errors.New("executor: scale down sem containers para remover")
	}

	target := members[len(members)-1]
	targetCPU := 0.0
	targetKnown := false

	for _, m := range members {
		stats, err := client.ContainerStats(ctx, m.ID)
		if err != nil {
			continue
		}
		cpu := stats.CPUPercent()
		if !targetKnown || cpu < targetCPU {
			target, targetCPU, targetKnown = m, cpu, true
		}
	}

	return target, nil
}

// Remove para (com timeout gracioso) e remove um container pelo ID.
func Remove(ctx context.Context, client *dockerclient.Client, containerID string) error {
	if err := client.StopContainer(ctx, containerID, stopTimeoutSeconds); err != nil {
		return fmt.Errorf("executor: parando container %s: %w", containerID[:12], err)
	}
	if err := client.RemoveContainer(ctx, containerID, false); err != nil {
		return fmt.Errorf("executor: removendo container %s: %w", containerID[:12], err)
	}
	return nil
}
