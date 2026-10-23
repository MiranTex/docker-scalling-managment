package platform

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"launcher/internal/dockerclient"
	"launcher/internal/store"
)

// ErrQuotaExceeded: o pedido passaria a quota do projeto ou a capacidade
// declarada do host.
var ErrQuotaExceeded = errors.New("quota excedida")

// Usage é o que está alocado (somado das labels de recursos dos
// containers vivos), não o consumo real.
type Usage struct {
	VCPU       float64 `json:"vcpu"`
	MemoryMB   int64   `json:"memoryMb"`
	Containers int     `json:"containers"`
}

func (s *Service) usage(ctx context.Context, label string) (Usage, error) {
	containers, err := s.docker.ListContainers(ctx, false, map[string][]string{"label": {label}})
	if err != nil {
		return Usage{}, fmt.Errorf("listando containers para contabilizar recursos: %w", err)
	}
	var u Usage
	for _, c := range containers {
		mem, err := strconv.ParseInt(c.Labels[dockerclient.LabelMemoryMB], 10, 64)
		if err != nil {
			continue
		}
		vcpu, _ := strconv.ParseFloat(c.Labels[dockerclient.LabelVCPU], 64)
		u.VCPU += vcpu
		u.MemoryMB += mem
		u.Containers++
	}
	return u, nil
}

// ProjectUsage é o alocado de um projeto, para a UI mostrar contra a quota.
func (s *Service) ProjectUsage(ctx context.Context, projectSlug string) (Usage, error) {
	p, err := s.GetProject(ctx, projectSlug)
	if err != nil {
		return Usage{}, err
	}
	return s.usage(ctx, LabelProjectID+"="+p.ID)
}

// checkQuota recusa alocar mais vcpu/memória se isso passar a quota do
// projeto (zero = sem quota) ou o teto de memória do host
// (LAUNCHER_HOST_MEMORY_MB, o mesmo do fluxo antigo de instâncias).
func (s *Service) checkQuota(ctx context.Context, p store.Project, vcpu float64, memoryMB int64) error {
	if p.QuotaVCPU > 0 || p.QuotaMemoryMB > 0 {
		u, err := s.usage(ctx, LabelProjectID+"="+p.ID)
		if err != nil {
			return err
		}
		if p.QuotaMemoryMB > 0 && u.MemoryMB+memoryMB > p.QuotaMemoryMB {
			return fmt.Errorf("%w: projeto %s já tem %d MB alocados + %d MB pedidos > quota de %d MB", ErrQuotaExceeded, p.Slug, u.MemoryMB, memoryMB, p.QuotaMemoryMB)
		}
		if p.QuotaVCPU > 0 && u.VCPU+vcpu > p.QuotaVCPU+1e-9 {
			return fmt.Errorf("%w: projeto %s já tem %.2f vCPU alocados + %.2f pedidos > quota de %.2f", ErrQuotaExceeded, p.Slug, u.VCPU, vcpu, p.QuotaVCPU)
		}
	}
	if s.cfg.HostMemoryMB > 0 {
		u, err := s.usage(ctx, dockerclient.PlatformLabel+"="+dockerclient.PlatformLabelValue)
		if err != nil {
			return err
		}
		ratio := s.cfg.MemoryOvercommitRatio
		if ratio <= 0 {
			ratio = 1
		}
		budget := int64(float64(s.cfg.HostMemoryMB) * ratio)
		if u.MemoryMB+memoryMB > budget {
			return fmt.Errorf("%w: o host já tem %d MB alocados + %d MB pedidos > teto de %d MB", ErrQuotaExceeded, u.MemoryMB, memoryMB, budget)
		}
	}
	return nil
}
