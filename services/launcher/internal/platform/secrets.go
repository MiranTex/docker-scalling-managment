package platform

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"launcher/internal/store"
)

// secretNameRe: nomes de segredo seguem o estilo de variáveis de ambiente,
// porque é quase sempre isso que viram.
var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// secretRefRe é a mesma sintaxe do fluxo antigo (secretsadmin): ${secret:NOME}.
var secretRefRe = regexp.MustCompile(`\$\{secret:([A-Za-z_][A-Za-z0-9_]*)\}`)

func (s *Service) ListSecrets(ctx context.Context, projectSlug, envSlug string) ([]store.SealedSecret, error) {
	_, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return nil, err
	}
	return s.db.ListSecrets(ctx, e.ID)
}

func (s *Service) PutSecret(ctx context.Context, projectSlug, envSlug, name, value, by string) error {
	_, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return err
	}
	if !secretNameRe.MatchString(name) {
		return invalid("nome de segredo %q inválido -- letras, números e \"_\", a começar por letra ou \"_\"", name)
	}
	if existing, err := s.db.GetSecret(ctx, e.ID, name); err == nil && existing.ManagedBy != "" {
		return invalid("o segredo %q é gerido pela plataforma (%s) e não pode ser alterado à mão", name, existing.ManagedBy)
	}
	return s.putSecret(ctx, e.ID, name, value, "", by)
}

// putSecret é o caminho interno, usado também pelos addons para guardar
// credenciais geradas (managedBy preenchido).
func (s *Service) putSecret(ctx context.Context, envID, name, value, managedBy, by string) error {
	if s.box == nil {
		return ErrSecretsDisabled
	}
	ct, nonce, err := s.box.Seal([]byte(value))
	if err != nil {
		return err
	}
	return s.db.PutSecret(ctx, store.SealedSecret{EnvironmentID: envID, Name: name, Ciphertext: ct, Nonce: nonce, ManagedBy: managedBy, UpdatedBy: by})
}

func (s *Service) DeleteSecret(ctx context.Context, projectSlug, envSlug, name string) error {
	_, e, err := s.Scope(ctx, projectSlug, envSlug)
	if err != nil {
		return err
	}
	if existing, err := s.db.GetSecret(ctx, e.ID, name); err == nil && existing.ManagedBy != "" {
		return invalid("o segredo %q é gerido pela plataforma (%s) -- apaga o %s em vez disso", name, existing.ManagedBy, existing.ManagedBy)
	}
	return mapStoreErr(s.db.DeleteSecret(ctx, e.ID, name))
}

// secretValues abre todos os segredos de um ambiente de uma vez -- um
// container com várias referências não deve fazer uma query por cada.
func (s *Service) secretValues(ctx context.Context, envID string) (map[string]string, error) {
	sealed, err := s.db.ListSecrets(ctx, envID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(sealed))
	if len(sealed) == 0 {
		return out, nil
	}
	if s.box == nil {
		return nil, ErrSecretsDisabled
	}
	for _, sec := range sealed {
		plain, err := s.box.Open(sec.Ciphertext, sec.Nonce)
		if err != nil {
			return nil, fmt.Errorf("abrindo segredo %q: %w", sec.Name, err)
		}
		out[sec.Name] = string(plain)
	}
	return out, nil
}

// ResolveEnv substitui cada ${secret:NOME} pelo valor do segredo do
// ambiente. Um segredo em falta é erro: arrancar uma app com uma password
// literal "${secret:DB_PASSWORD}" é pior do que não arrancar.
func (s *Service) ResolveEnv(ctx context.Context, envID string, entries []string) ([]string, error) {
	needs := false
	for _, e := range entries {
		if strings.Contains(e, "${secret:") {
			needs = true
			break
		}
	}
	if !needs {
		return entries, nil
	}
	values, err := s.secretValues(ctx, envID)
	if err != nil {
		return nil, err
	}
	return resolveWith(values, entries)
}

// resolveWith é ResolveEnv com os valores já abertos.
func resolveWith(values map[string]string, entries []string) ([]string, error) {
	out := make([]string, len(entries))
	var missing []string
	for i, e := range entries {
		out[i] = secretRefRe.ReplaceAllStringFunc(e, func(ref string) string {
			name := secretRefRe.FindStringSubmatch(ref)[1]
			v, ok := values[name]
			if !ok {
				missing = append(missing, name)
			}
			return v
		})
	}
	if len(missing) > 0 {
		return nil, invalid("segredos em falta neste ambiente: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
