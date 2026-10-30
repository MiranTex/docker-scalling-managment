package httpapi

import (
	"net/http"
	"time"

	"auth/internal/password"
)

// Trocar passwords: um super-admin define a de qualquer conta; cada pessoa
// troca a sua (com a atual). Nos dois casos as sessões dessa conta são
// revogadas -- quem estava dentro (noutro browser, ou quem a roubou) tem
// de voltar a entrar.

type setPasswordRequest struct {
	Password string `json:"password"`
}

func (h *Handler) handleSetUserPassword(w http.ResponseWriter, r *http.Request, adminID string) {
	var req setPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Password) < minPasswordLength {
		writeError(w, http.StatusBadRequest, "weak_password", "senha precisa de pelo menos 8 caracteres")
		return
	}
	id := r.PathValue("id")
	if !h.setPassword(w, r, id, req.Password) {
		return
	}
	h.logger.Info("password definida por um administrador", "user", id, "admin", adminID)
	w.WriteHeader(http.StatusNoContent)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (h *Handler) handleChangeOwnPassword(w http.ResponseWriter, r *http.Request, userID string) {
	var req changePasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.NewPassword) < minPasswordLength {
		writeError(w, http.StatusBadRequest, "weak_password", "senha precisa de pelo menos 8 caracteres")
		return
	}
	user, found, err := h.users.FindUserByID(r.Context(), userID)
	if err != nil || !found {
		writeError(w, http.StatusUnauthorized, "unauthorized", "conta não encontrada")
		return
	}
	_, hash, err := h.users.FindUserByEmailWithPassword(r.Context(), user.Email)
	if err != nil || hash == "" || password.Verify(req.CurrentPassword, hash) != nil {
		writeError(w, http.StatusForbidden, "wrong_password", "a password atual não está certa")
		return
	}
	if !h.setPassword(w, r, userID, req.NewPassword) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// setPassword grava a password nova e revoga as sessões; escreve o erro e
// devolve false se não conseguir.
func (h *Handler) setPassword(w http.ResponseWriter, r *http.Request, userID, plain string) bool {
	hash, err := password.Hash(plain)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro processando senha")
		return false
	}
	found, err := h.userAdmin.SetUserPassword(r.Context(), userID, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "erro gravando a password")
		return false
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "utilizador não encontrado")
		return false
	}
	if err := h.userAdmin.RevokeUserSessions(r.Context(), userID, time.Now()); err != nil {
		h.logger.Error("revogando sessões depois de trocar a password", "user", userID, "err", err)
	}
	return true
}
