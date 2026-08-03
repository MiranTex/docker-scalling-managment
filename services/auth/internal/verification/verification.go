// Package verification implementa a prova de posse de um e-mail: um
// token opaco de uso único, enviado (fora deste pacote -- ver
// httpapi/cmd/authd) para o endereço que se quer confirmar. Se alguém
// consegue apresentar o token, é porque teve acesso à caixa de entrada
// daquele e-mail -- é essa a prova.
//
// Mesmo padrão de segredo dos refresh tokens e API keys: só o hash é
// persistido, nunca o valor em si.
package verification

import (
	"context"
	"errors"
	"fmt"
	"time"

	"auth/internal/idgen"
	"auth/internal/opaquetoken"
)

// randomSecretBytes segue o mesmo raciocínio dos outros segredos opacos
// deste serviço.
const randomSecretBytes = 32

// Verification é um token de verificação tal como persistido.
type Verification struct {
	ID         string
	UserID     string
	TokenHash  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
}

// Store é a persistência necessária pelo Manager.
type Store interface {
	Create(ctx context.Context, v Verification) error
	FindByHash(ctx context.Context, tokenHash string) (Verification, bool, error)
	Consume(ctx context.Context, id string, consumedAt time.Time) error
	MarkUserVerified(ctx context.Context, userID string, verifiedAt time.Time) error
}

// ErrInvalid é devolvido quando o token apresentado não existe, já foi
// usado, ou expirou.
var ErrInvalid = errors.New("verification: token inválido, já usado ou expirado")

// Manager emite e confirma tokens de verificação de e-mail.
type Manager struct {
	store Store
	ttl   time.Duration
}

// NewManager cria um Manager que persiste em store e emite tokens válidos
// por ttl.
func NewManager(store Store, ttl time.Duration) *Manager {
	return &Manager{store: store, ttl: ttl}
}

// Issue cria um novo token de verificação para userID. Devolve o segredo
// em texto plano -- quem chama é responsável por o entregar ao dono do
// e-mail (nunca devolver isto na resposta de uma API pública: o objetivo
// inteiro é provar que só quem tem acesso à caixa de entrada consegue
// este valor).
func (m *Manager) Issue(ctx context.Context, userID string) (plaintext string, err error) {
	secret, err := opaquetoken.New(randomSecretBytes)
	if err != nil {
		return "", fmt.Errorf("verification: gerando segredo: %w", err)
	}
	id, err := idgen.New()
	if err != nil {
		return "", fmt.Errorf("verification: gerando id: %w", err)
	}

	v := Verification{
		ID:        id,
		UserID:    userID,
		TokenHash: opaquetoken.Hash(secret),
		ExpiresAt: time.Now().Add(m.ttl),
	}
	if err := m.store.Create(ctx, v); err != nil {
		return "", fmt.Errorf("verification: persistindo token: %w", err)
	}
	return secret, nil
}

// Verify confirma o token apresentado: se válido, marca o e-mail do
// utilizador como verificado e consome o token (não pode ser reusado),
// devolvendo o ID do utilizador verificado.
func (m *Manager) Verify(ctx context.Context, presented string) (userID string, err error) {
	found, ok, err := m.store.FindByHash(ctx, opaquetoken.Hash(presented))
	if err != nil {
		return "", fmt.Errorf("verification: procurando token: %w", err)
	}
	if !ok || found.ConsumedAt != nil || time.Now().After(found.ExpiresAt) {
		return "", ErrInvalid
	}

	now := time.Now()
	if err := m.store.Consume(ctx, found.ID, now); err != nil {
		return "", fmt.Errorf("verification: consumindo token: %w", err)
	}
	if err := m.store.MarkUserVerified(ctx, found.UserID, now); err != nil {
		return "", fmt.Errorf("verification: marcando utilizador como verificado: %w", err)
	}
	return found.UserID, nil
}
