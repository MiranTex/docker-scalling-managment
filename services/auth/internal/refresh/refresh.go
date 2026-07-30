// Package refresh implementa refresh tokens opacos (não são JWT) com
// rotação e deteção de reuso — o padrão hoje recomendado pela OWASP pra
// sessões de longa duração.
//
// Ao contrário do access token (JWT, autocontido, validado offline), um
// refresh token é só um segredo aleatório: o Manager guarda apenas o seu
// hash (nunca o valor em si), e cada vez que é usado para pedir um novo
// access token, é IMEDIATAMENTE revogado e substituído por um novo — o
// cliente nunca reutiliza o mesmo refresh token duas vezes. Se um token já
// revogado for apresentado de novo, é sinal de que foi roubado (alguém
// copiou o token antigo antes da rotação), e o Manager revoga toda a
// "família" de tokens daquele login — encerrando a sessão inteira, não só
// o token comprometido.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"time"

	"auth/internal/idgen"
	"auth/internal/opaquetoken"
)

// Token é um refresh token tal como persistido — nunca guarda o segredo em
// si, só o seu hash (TokenHash), igual a como senhas nunca são guardadas em
// texto plano.
type Token struct {
	ID        string
	UserID    string
	FamilyID  string
	TokenHash string
	ExpiresAt time.Time
	RevokedAt *time.Time
}

// Store é a persistência necessária pro Manager — implementada pelo
// pacote store (Postgres) em produção, e por um fake em memória nos testes
// deste pacote (ver refresh_test.go), o que permite testar toda a lógica
// de rotação/deteção de reuso sem precisar de uma base de dados real.
type Store interface {
	Create(ctx context.Context, t Token) error
	FindByHash(ctx context.Context, tokenHash string) (Token, bool, error)
	Revoke(ctx context.Context, id string, revokedAt time.Time) error
	RevokeFamily(ctx context.Context, familyID string, revokedAt time.Time) error
}

// ErrInvalid é devolvido quando o token apresentado não existe, expirou, ou
// tem formato inválido.
var ErrInvalid = errors.New("refresh: token inválido ou expirado")

// ErrReuseDetected é devolvido quando o token apresentado já tinha sido
// usado (rodado) antes — sinal de possível roubo. Quem chama deve tratar
// isto como um evento de segurança (ex: registar/alertar), não só como um
// login inválido comum: a sessão inteira já foi revogada quando este erro
// é devolvido.
var ErrReuseDetected = errors.New("refresh: reuso detectado, sessão revogada")

// Manager emite e roda refresh tokens.
type Manager struct {
	store Store
	ttl   time.Duration
}

// NewManager cria um Manager que persiste em store e emite tokens válidos
// por ttl.
func NewManager(store Store, ttl time.Duration) *Manager {
	return &Manager{store: store, ttl: ttl}
}

// Issue cria a primeira geração de refresh token de uma nova sessão (ex:
// login bem-sucedido), devolvendo o segredo em texto plano (a única vez
// que ele existe fora do hash guardado) — é isso que o cliente vai
// apresentar depois em Rotate/Revoke.
func (m *Manager) Issue(ctx context.Context, userID string) (plaintext string, _ error) {
	familyID, err := idgen.New()
	if err != nil {
		return "", fmt.Errorf("refresh: gerando family id: %w", err)
	}
	return m.issueInFamily(ctx, userID, familyID)
}

func (m *Manager) issueInFamily(ctx context.Context, userID, familyID string) (string, error) {
	secret, err := randomSecret()
	if err != nil {
		return "", err
	}
	id, err := idgen.New()
	if err != nil {
		return "", fmt.Errorf("refresh: gerando id: %w", err)
	}

	t := Token{
		ID:        id,
		UserID:    userID,
		FamilyID:  familyID,
		TokenHash: hashToken(secret),
		ExpiresAt: time.Now().Add(m.ttl),
	}
	if err := m.store.Create(ctx, t); err != nil {
		return "", fmt.Errorf("refresh: persistindo token: %w", err)
	}
	return secret, nil
}

// Rotate troca um refresh token válido por um novo, revogando o
// apresentado no mesmo golpe -- é assim que se pede um novo access token
// quando o antigo expira. Devolve ErrReuseDetected (e revoga toda a
// família) se o token apresentado já tinha sido rodado antes.
func (m *Manager) Rotate(ctx context.Context, presented string) (newPlaintext string, userID string, err error) {
	found, ok, err := m.store.FindByHash(ctx, hashToken(presented))
	if err != nil {
		return "", "", fmt.Errorf("refresh: procurando token: %w", err)
	}
	if !ok {
		return "", "", ErrInvalid
	}
	if found.RevokedAt != nil {
		if revokeErr := m.store.RevokeFamily(ctx, found.FamilyID, time.Now()); revokeErr != nil {
			return "", "", fmt.Errorf("refresh: revogando família após reuso: %w", revokeErr)
		}
		return "", "", ErrReuseDetected
	}
	if time.Now().After(found.ExpiresAt) {
		return "", "", ErrInvalid
	}

	if err := m.store.Revoke(ctx, found.ID, time.Now()); err != nil {
		return "", "", fmt.Errorf("refresh: revogando token usado: %w", err)
	}

	next, err := m.issueInFamily(ctx, found.UserID, found.FamilyID)
	if err != nil {
		return "", "", err
	}
	return next, found.UserID, nil
}

// Revoke encerra a sessão associada ao token apresentado (ex: logout) --
// não é erro chamar com um token já inválido/inexistente, o resultado
// final (sessão terminada) é o mesmo.
func (m *Manager) Revoke(ctx context.Context, presented string) error {
	found, ok, err := m.store.FindByHash(ctx, hashToken(presented))
	if err != nil {
		return fmt.Errorf("refresh: procurando token: %w", err)
	}
	if !ok || found.RevokedAt != nil {
		return nil
	}
	if err := m.store.Revoke(ctx, found.ID, time.Now()); err != nil {
		return fmt.Errorf("refresh: revogando token: %w", err)
	}
	return nil
}

// randomSecretBytes é o tamanho, em bytes, do segredo aleatório por trás
// de cada refresh token -- 32 bytes (256 bits) é bem acima do que qualquer
// ataque de força bruta online consegue tentar adivinhar.
const randomSecretBytes = 32

func randomSecret() (string, error) {
	return opaquetoken.New(randomSecretBytes)
}

func hashToken(secret string) string {
	return opaquetoken.Hash(secret)
}
