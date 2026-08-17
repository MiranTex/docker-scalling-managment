package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dbadmin/internal/audit"
	"dbadmin/internal/provisioning"
)

type fakeProvisioner struct {
	deleted string
}

func (f *fakeProvisioner) List(context.Context) ([]provisioning.Schema, error) {
	return nil, nil
}

func (f *fakeProvisioner) Create(context.Context, string, string, string) (provisioning.Credentials, error) {
	return provisioning.Credentials{}, nil
}

func (f *fakeProvisioner) ResetPassword(context.Context, string) (provisioning.Credentials, error) {
	return provisioning.Credentials{}, nil
}

func (f *fakeProvisioner) Delete(_ context.Context, name string) error {
	f.deleted = name
	return nil
}

func TestDeleteSchemaRequiresMatchingConfirmation(t *testing.T) {
	provisioner := &fakeProvisioner{}
	handler := &Handler{
		schemas: provisioner,
		audit:   audit.NewLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}

	request := httptest.NewRequest(http.MethodDelete, "/v1/schemas/orders", strings.NewReader(`{"confirmation":"wrong"}`))
	request.SetPathValue("name", "orders")
	response := httptest.NewRecorder()
	handler.handleDeleteSchema(response, request, "admin")

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
	if provisioner.deleted != "" {
		t.Fatalf("delete was called despite mismatched confirmation")
	}
}

func TestDeleteSchemaReturnsNoContent(t *testing.T) {
	provisioner := &fakeProvisioner{}
	handler := &Handler{
		schemas: provisioner,
		audit:   audit.NewLogger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}

	request := httptest.NewRequest(http.MethodDelete, "/v1/schemas/orders", strings.NewReader(`{"confirmation":"orders"}`))
	request.SetPathValue("name", "orders")
	response := httptest.NewRecorder()
	handler.handleDeleteSchema(response, request, "admin")

	if response.Code != http.StatusNoContent || provisioner.deleted != "orders" {
		t.Fatalf("expected successful delete, got status=%d deleted=%q", response.Code, provisioner.deleted)
	}
}
