package platform

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// Etiquetas: os nós têm-nas (ex: gpu=nvidia, disco=ssd) e um serviço pode
// exigir algumas -- as réplicas dele só vão para nós que tenham todas.
// Umas põe-nas um admin; outras a plataforma deteta (detectLabels).

var (
	labelKeyRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,62})$`)
	labelValueRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{0,63}$`)
)

const maxLabels = 20

func validateLabels(what string, labels map[string]string) error {
	if len(labels) > maxLabels {
		return invalid("%s: no máximo %d etiquetas", what, maxLabels)
	}
	for k, v := range labels {
		if !labelKeyRe.MatchString(k) {
			return invalid("%s: etiqueta %q inválida -- minúsculas, dígitos, \".\", \"_\" ou \"-\", até 63 caracteres", what, k)
		}
		if !labelValueRe.MatchString(v) {
			return invalid("%s: valor %q de %s inválido -- letras, dígitos, \".\", \"_\" ou \"-\", até 63 caracteres", what, v, k)
		}
	}
	return nil
}

// detectLabels: o que se sabe do nó pelo Docker dele. gpu=nvidia quando
// tem o runtime da NVIDIA (o NVIDIA Container Toolkit instalado) -- só
// então um container consegue usar a placa.
func detectLabels(info *dockerclient.SystemInfo) map[string]string {
	labels := map[string]string{}
	if _, ok := info.Runtimes["nvidia"]; ok {
		labels["gpu"] = "nvidia"
	}
	return labels
}

// missingLabels: as etiquetas exigidas que o nó não tem, como "k=v".
func missingLabels(node, required map[string]string) []string {
	var out []string
	for k, v := range required {
		if have, ok := node[k]; !ok || have != v {
			out = append(out, k+"="+v)
		}
	}
	sort.Strings(out)
	return out
}

// SetNodeLabels troca as etiquetas que um admin pôs no nó. As réplicas que
// já lá correm ficam; as novas seguem as etiquetas novas.
func (s *Service) SetNodeLabels(ctx context.Context, name string, labels map[string]string) (NodeView, error) {
	if err := validateLabels("nó "+name, labels); err != nil {
		return NodeView{}, err
	}
	if err := s.db.SetNodeLabels(ctx, name, labels); err != nil {
		if errors.Is(err, store.ErrNodeNotFound) {
			return NodeView{}, fmt.Errorf("%w: nó %s", ErrNotFound, name)
		}
		return NodeView{}, err
	}
	// Um serviço à espera de um nó com estas etiquetas pode avançar já.
	s.Kick()
	return s.nodeView(ctx, name)
}

func (s *Service) nodeView(ctx context.Context, name string) (NodeView, error) {
	nodes, err := s.ListNodes(ctx)
	if err != nil {
		return NodeView{}, err
	}
	for _, n := range nodes {
		if n.Name == name {
			return n, nil
		}
	}
	return NodeView{}, fmt.Errorf("%w: nó %s", ErrNotFound, name)
}

func FormatLabels(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
