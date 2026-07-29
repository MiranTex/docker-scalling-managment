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

// scaleDown escolhe e remove imediatamente um container do grupo. Usado
// pelo fluxo simples (sem drain do load balancer); cmd/group usa
// SelectScaleDownTarget + Remove separadamente para tirar o alvo dos
// backends antes de pará-lo.
func scaleDown(ctx context.Context, client *dockerclient.Client, members []dockerclient.Container) error {
	target, err := SelectScaleDownTarget(members)
	if err != nil {
		return err
	}
	return Remove(ctx, client, target.ID)
}

// SelectScaleDownTarget escolhe qual container seria removido num scale
// down, sem removê-lo. A ordem de /containers/json não é garantida
// cronologicamente, então essa escolha (o último da lista) é só um ponto de
// partida simples — critérios melhores (mais recente, menos carregado) podem
// vir depois. Separado de Remove para permitir tirar o alvo dos backends do
// load balancer antes de efetivamente pará-lo (evita rotear requisições para
// um container que já está de saída).
func SelectScaleDownTarget(members []dockerclient.Container) (dockerclient.Container, error) {
	if len(members) == 0 {
		return dockerclient.Container{}, errors.New("executor: scale down sem containers para remover")
	}
	return members[len(members)-1], nil
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
