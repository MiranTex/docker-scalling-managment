package httpapi

// Isolamento entre a API antiga (instâncias, groups, redes) e os projetos:
// as redes e os containers de um projeto pertencem a esse projeto e são
// geridos só pela API de plataforma (internal/platformapi). Sem isto, um
// admin podia ligar um container qualquer à rede de um projeto, ou lançar
// uma instância lá dentro, e chegar às bases de dados dele.

import (
	"context"
	"fmt"

	"launcher/internal/platform"
)

// isProjectResource reconhece uma rede ou container de projeto pela label
// que internal/platform põe em tudo o que cria.
func isProjectResource(labels map[string]string) bool {
	return labels[platform.LabelProjectID] != ""
}

// checkNotProjectNetwork recusa uma rede de projeto. Uma rede que não
// existe passa -- o Docker dá o erro certo mais à frente.
func (h *Handler) checkNotProjectNetwork(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	detail, err := h.docker.InspectNetwork(ctx, name)
	if err != nil {
		return nil
	}
	if isProjectResource(detail.Labels) {
		return fmt.Errorf("a rede %q pertence a um projeto -- é gerida em Projetos, não aqui", name)
	}
	return nil
}
