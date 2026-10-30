package platform

import (
	"context"
	"net"
	"strconv"

	"launcher/internal/store"
)

// AddonTunnelAddress é onde o launcher liga para encaminhar um túnel até
// ao addon: o nome do container na rede do ambiente, a que o launcher
// está ligado (ver Config.SelfContainer). Confirma, como o terminal, que
// o container é deste ambiente e está a correr.
func (s *Service) AddonTunnelAddress(ctx context.Context, projectSlug, envSlug, addonSlug string) (TerminalTarget, string, error) {
	t, err := s.AddonTerminalTarget(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return TerminalTarget{}, "", err
	}
	a, err := s.db.GetAddonBySlug(ctx, t.Environment.ID, addonSlug)
	if err != nil {
		return TerminalTarget{}, "", mapStoreErr(err)
	}
	return t, net.JoinHostPort(t.Container, strconv.Itoa(addonPort(a.Kind))), nil
}

// AddonCredentials é o que um cliente precisa para entrar no addon (pela
// CLI, através de um túnel). Revela a password: a API só a dá a quem
// pode abrir um terminal no ambiente, que já a vê no env.
type AddonCredentials struct {
	Kind     string `json:"kind"`
	User     string `json:"user"`
	Password string `json:"password"`
	// Database: o nome da base (Postgres), o número (Redis) ou o bucket
	// (storage).
	Database string `json:"database"`
	Port     int    `json:"port"`
	Region   string `json:"region,omitempty"`
}

func (s *Service) AddonCredentials(ctx context.Context, projectSlug, envSlug, addonSlug string) (AddonCredentials, error) {
	_, e, a, err := s.addonScope(ctx, projectSlug, envSlug, addonSlug)
	if err != nil {
		return AddonCredentials{}, err
	}
	user, password, err := s.addonLogin(ctx, e.ID, a)
	if err != nil {
		return AddonCredentials{}, err
	}
	c := AddonCredentials{Kind: a.Kind, Password: password, Port: addonPort(a.Kind)}
	switch a.Kind {
	case store.AddonRedis:
		c.User, c.Database = "default", "0"
	case store.AddonStorage:
		c.User, c.Database, c.Region = user, storageBucket, storageRegion
	default:
		c.User, c.Database = addonPGUser, addonPGDatabase
	}
	return c, nil
}
