package dockerclient

import "context"

// imageSummary é o subconjunto de /images/json que precisamos: só os
// nomes/tags (RepoTags) já buildados/pulled no daemon local.
type imageSummary struct {
	RepoTags []string `json:"RepoTags"`
}

// ListImages devolve as referências "repo:tag" de toda imagem conhecida
// pelo daemon Docker local, na ordem em que o Engine as devolve.
// Imagens sem tag (RepoTags == ["<none>:<none>"], ex: camadas
// intermediárias de build) são omitidas -- não fazem sentido como
// escolha de imagem para um launch template.
func (c *Client) ListImages(ctx context.Context) ([]string, error) {
	var summaries []imageSummary
	if err := c.do(ctx, "GET", "/images/json", nil, &summaries); err != nil {
		return nil, err
	}

	var refs []string
	for _, s := range summaries {
		for _, tag := range s.RepoTags {
			if tag == "<none>:<none>" {
				continue
			}
			refs = append(refs, tag)
		}
	}
	return refs, nil
}
