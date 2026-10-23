package dockerclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Volume é o subconjunto de /volumes que interessa: o nome e as labels --
// é pelas labels que se sabe se o volume pertence a uma stack compose
// (ver internal/httpapi, SpecPolicy).
type Volume struct {
	Name      string            `json:"Name"`
	Labels    map[string]string `json:"Labels"`
	Driver    string            `json:"Driver"`
	CreatedAt string            `json:"CreatedAt"`
	// UsageData só vem preenchido em /system/df (ver VolumesWithUsage).
	UsageData *struct {
		Size     int64 `json:"Size"`     // bytes; -1 = desconhecido
		RefCount int64 `json:"RefCount"` // containers que o montam
	} `json:"UsageData,omitempty"`
}

// FindVolume devolve o volume com exatamente este nome, ou nil se ainda
// não existir (o Docker cria-o vazio no primeiro mount). Usa a listagem
// filtrada em vez de /volumes/{name} para "não existe" não ser um erro --
// o filtro "name" do Docker casa por substring, daí a comparação exata.
func (c *Client) FindVolume(ctx context.Context, name string) (*Volume, error) {
	filters, err := json.Marshal(map[string][]string{"name": {name}})
	if err != nil {
		return nil, fmt.Errorf("dockerclient: encoding filters: %w", err)
	}
	var resp struct {
		Volumes []Volume `json:"Volumes"`
	}
	if err := c.do(ctx, "GET", "/volumes?filters="+url.QueryEscape(string(filters)), nil, &resp); err != nil {
		return nil, err
	}
	for _, v := range resp.Volumes {
		if v.Name == name {
			return &v, nil
		}
	}
	return nil, nil
}

// CreateVolume cria um volume nomeado com labels (PlatformLabel incluída).
// Criar um que já existe não é erro para o Docker -- devolve o existente.
func (c *Client) CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	all := map[string]string{PlatformLabel: PlatformLabelValue}
	for k, v := range labels {
		all[k] = v
	}
	return c.do(ctx, "POST", "/volumes/create", map[string]any{"Name": name, "Labels": all}, nil)
}

// RemoveVolume apaga um volume. Não existir não é erro (idempotente).
func (c *Client) RemoveVolume(ctx context.Context, name string) error {
	err := c.do(ctx, "DELETE", "/volumes/"+url.PathEscape(name), nil, nil)
	if err != nil && strings.Contains(err.Error(), "status 404") {
		return nil
	}
	return err
}

// ListVolumesByLabel devolve os volumes com esta label ("chave=valor").
func (c *Client) ListVolumesByLabel(ctx context.Context, label string) ([]Volume, error) {
	filters, err := json.Marshal(map[string][]string{"label": {label}})
	if err != nil {
		return nil, fmt.Errorf("dockerclient: encoding filters: %w", err)
	}
	var resp struct {
		Volumes []Volume `json:"Volumes"`
	}
	if err := c.do(ctx, "GET", "/volumes?filters="+url.QueryEscape(string(filters)), nil, &resp); err != nil {
		return nil, err
	}
	return resp.Volumes, nil
}

// VolumesWithUsage lista todos os volumes com o espaço que ocupam. É a
// mesma conta do `docker system df -v` -- percorre o disco, por isso não
// se chama em ciclo, só quando alguém abre a lista.
func (c *Client) VolumesWithUsage(ctx context.Context) ([]Volume, error) {
	var resp struct {
		Volumes []Volume `json:"Volumes"`
	}
	if err := c.do(ctx, "GET", "/system/df?type=volume", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Volumes, nil
}
