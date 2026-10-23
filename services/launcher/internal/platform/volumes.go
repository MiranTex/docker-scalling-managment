package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"launcher/internal/store"
)

// Estados de um volume na página Volumes.
const (
	VolumeInUse  = "in-use" // um container (a correr ou parado) monta-o
	VolumeFree   = "free"   // ninguém o monta, mas o dono ainda existe
	VolumeOrphan = "orphan" // o dono (addon/ambiente) já foi apagado
)

// VolumeView é uma linha da página Volumes.
type VolumeView struct {
	Name      string `json:"name"`
	CreatedAt string `json:"createdAt"`
	// SizeBytes -1 = o Docker não soube calcular.
	SizeBytes   int64  `json:"sizeBytes"`
	Status      string `json:"status"`
	Project     string `json:"project"`
	Environment string `json:"environment,omitempty"`
	// Addon e Kind ("data"/"backups") quando o volume é de um addon.
	Addon string `json:"addon,omitempty"`
	Kind  string `json:"kind,omitempty"`
	// Containers que o montam e onde.
	UsedBy []VolumeUse `json:"usedBy"`
	// Deletable + Reason: se pode ser apagado e, se não, porquê -- a mesma
	// regra que DeleteVolume volta a aplicar.
	Deletable bool   `json:"deletable"`
	Reason    string `json:"reason,omitempty"`
}

type VolumeUse struct {
	Container string `json:"container"`
	State     string `json:"state"`
	Path      string `json:"path"`
}

// volumeKind deduz o papel de um volume de addon pelo sufixo do nome (ver
// addonVolume) -- os volumes não guardam esta informação noutro sítio.
func volumeKind(name string) string {
	switch {
	case strings.HasSuffix(name, "-data"):
		return "data"
	case strings.HasSuffix(name, "-backups"):
		return "backups"
	}
	return ""
}

// ListVolumes lista só volumes de projetos -- os da própria plataforma e
// os de outras apps do host não são assunto desta página.
func (s *Service) ListVolumes(ctx context.Context) ([]VolumeView, error) {
	vols, err := s.docker.VolumesWithUsage(ctx)
	if err != nil {
		return nil, fmt.Errorf("listando volumes: %w", err)
	}
	// Todos os containers do host, não só os de projeto: um volume montado
	// por qualquer container está em uso, e o Docker recusaria apagá-lo.
	containers, err := s.docker.ListContainers(ctx, true, nil)
	if err != nil {
		return nil, fmt.Errorf("listando containers: %w", err)
	}
	usedBy := map[string][]VolumeUse{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name != "" {
				usedBy[m.Name] = append(usedBy[m.Name], VolumeUse{Container: containerName(c), State: c.State, Path: m.Destination})
			}
		}
	}

	addons, err := s.db.ListAllAddons(ctx)
	if err != nil {
		return nil, err
	}
	addonByID := map[string]store.Addon{}
	for _, a := range addons {
		addonByID[a.ID] = a
	}
	envs, err := s.db.ListAllEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	envExists := map[string]bool{}
	for _, e := range envs {
		envExists[e.ID] = true
	}

	out := []VolumeView{}
	for _, v := range vols {
		if v.Labels[LabelProjectID] == "" {
			continue
		}
		view := VolumeView{
			Name: v.Name, CreatedAt: v.CreatedAt, SizeBytes: -1,
			Project: v.Labels[LabelProject], Environment: v.Labels[LabelEnvironment],
			UsedBy: usedBy[v.Name],
		}
		if view.UsedBy == nil {
			view.UsedBy = []VolumeUse{}
		}
		if v.UsageData != nil {
			view.SizeBytes = v.UsageData.Size
		}
		addonID := v.Labels[LabelAddon]
		if addonID != "" {
			view.Kind = volumeKind(v.Name)
		}
		addon, addonExists := addonByID[addonID]
		if addonExists {
			view.Addon = addon.Slug
		}

		switch {
		case len(view.UsedBy) > 0:
			view.Status, view.Reason = VolumeInUse, "está montado por um container"
		case addonID != "" && !addonExists, !envExists[v.Labels[LabelEnvironmentID]]:
			view.Status = VolumeOrphan
		default:
			view.Status = VolumeFree
		}
		// O volume de dados de um addon que existe só se apaga apagando o
		// addon: se o container estiver a ser recriado pelo reconciliador,
		// apagá-lo nessa janela deixava a base vazia.
		if view.Status != VolumeInUse && addonExists && view.Kind == "data" {
			view.Reason = fmt.Sprintf("são os dados do addon %s, que ainda existe -- apaga o addon com \"apagar dados\"", addon.Slug)
		}
		view.Deletable = view.Reason == ""
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Project != out[j].Project {
			return out[i].Project < out[j].Project
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// DeleteVolume apaga um volume se a regra da lista o permitir --
// recalculada aqui, não confiada ao que a UI mostrou.
func (s *Service) DeleteVolume(ctx context.Context, name string) error {
	vols, err := s.ListVolumes(ctx)
	if err != nil {
		return err
	}
	for _, v := range vols {
		if v.Name != name {
			continue
		}
		if !v.Deletable {
			return invalid("não é possível apagar %s: %s", name, v.Reason)
		}
		if err := s.docker.RemoveVolume(ctx, name); err != nil {
			// Corrida: alguém montou-o entre a verificação e o pedido.
			if strings.Contains(err.Error(), "in use") {
				return invalid("não é possível apagar %s: está montado por um container", name)
			}
			return err
		}
		s.logger.Info("volume apagado", "volume", name, "status", v.Status)
		return nil
	}
	return fmt.Errorf("%w: volume %s", ErrNotFound, name)
}
