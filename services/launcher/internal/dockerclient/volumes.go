package dockerclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// Volume é o subconjunto de /volumes que interessa: o nome e as labels --
// é pelas labels que se sabe se o volume pertence a uma stack compose
// (ver internal/httpapi, SpecPolicy).
type Volume struct {
	Name   string            `json:"Name"`
	Labels map[string]string `json:"Labels"`
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
