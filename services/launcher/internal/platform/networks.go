package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Tipos de container numa rede de ambiente, para a UI os distinguir.
const (
	NetMemberService  = "service"
	NetMemberAddon    = "addon"
	NetMemberJob      = "job"
	NetMemberPlatform = "platform"
	// NetMemberOther não devia existir: um container que não é deste
	// ambiente nem da plataforma, ligado à mão. Mostra-se para se ver.
	NetMemberOther = "other"
)

type NetworkMember struct {
	Container string `json:"container"`
	Kind      string `json:"kind"`
	// Name é o serviço/addon (slug) ou o papel na plataforma.
	Name  string `json:"name,omitempty"`
	State string `json:"state,omitempty"`
}

// EnvironmentNetwork é a rede de um ambiente e tudo o que está ligado a ela.
type EnvironmentNetwork struct {
	Name    string          `json:"name"`
	Subnet  string          `json:"subnet,omitempty"`
	Gateway string          `json:"gateway,omitempty"`
	Members []NetworkMember `json:"members"`
}

// platformRole diz o que um container da plataforma faz nesta rede.
func (s *Service) platformRole(name, id string) string {
	switch {
	case s.cfg.TraefikContainer != "" && name == s.cfg.TraefikContainer:
		return "Traefik (encaminha o tráfego)"
	case s.cfg.PrometheusContainer != "" && name == s.cfg.PrometheusContainer:
		return "Prometheus (recolhe métricas)"
	// O launcher é configurado pelo nome ou pelo id curto (hostname).
	case s.cfg.SelfContainer != "" && (name == s.cfg.SelfContainer || strings.HasPrefix(id, s.cfg.SelfContainer)):
		return "launcher (health checks)"
	}
	return ""
}

func (s *Service) GetEnvironmentNetwork(ctx context.Context, projectSlug, envSlug string) (EnvironmentNetwork, error) {
	_, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return EnvironmentNetwork{}, err
	}
	detail, err := s.docker.InspectNetwork(ctx, e.Network)
	if err != nil {
		return EnvironmentNetwork{}, fmt.Errorf("inspecionando a rede %s: %w", e.Network, err)
	}
	// Labels e estado dos containers do ambiente, para classificar cada
	// membro sem um inspect por container.
	containers, err := s.docker.ListContainers(ctx, true, map[string][]string{"label": {LabelEnvironmentID + "=" + e.ID}})
	if err != nil {
		return EnvironmentNetwork{}, err
	}
	labelsByName := map[string]map[string]string{}
	stateByName := map[string]string{}
	for _, c := range containers {
		labelsByName[containerName(c)] = c.Labels
		stateByName[containerName(c)] = c.State
	}

	out := EnvironmentNetwork{Name: detail.Name, Subnet: detail.Subnet, Gateway: detail.Gateway, Members: []NetworkMember{}}
	for _, m := range detail.Containers {
		member := NetworkMember{Container: m.Name, State: stateByName[m.Name]}
		labels, ours := labelsByName[m.Name]
		switch {
		case ours && labels[LabelRole] == RoleApp:
			member.Kind, member.Name = NetMemberService, labels[LabelServiceSlug]
		case ours && labels[LabelRole] == RoleAddon:
			member.Kind, member.Name = NetMemberAddon, labels[LabelServiceSlug]
		case ours && labels[LabelRole] == RoleJob:
			member.Kind = NetMemberJob
		case s.platformRole(m.Name, m.ContainerID) != "":
			member.Kind, member.Name = NetMemberPlatform, s.platformRole(m.Name, m.ContainerID)
		default:
			member.Kind = NetMemberOther
		}
		out.Members = append(out.Members, member)
	}
	order := map[string]int{NetMemberService: 0, NetMemberAddon: 1, NetMemberJob: 2, NetMemberPlatform: 3, NetMemberOther: 4}
	sort.Slice(out.Members, func(i, j int) bool {
		a, b := out.Members[i], out.Members[j]
		if order[a.Kind] != order[b.Kind] {
			return order[a.Kind] < order[b.Kind]
		}
		return a.Container < b.Container
	})
	return out, nil
}
