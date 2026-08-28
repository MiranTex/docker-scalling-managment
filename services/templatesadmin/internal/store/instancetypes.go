package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// InstanceType é uma entrada do catálogo de tamanhos de container -- o
// equivalente local a um instance type da AWS. O launcher traduz estes
// valores para limites reais do Docker no momento de criar o container
// (ver services/launcher/internal/dockerclient.Resources); este serviço
// só guarda e serve o catálogo.
type InstanceType struct {
	Name        string  `json:"name"`
	DisplayName string  `json:"displayName"`
	VCPU        float64 `json:"vcpu"`
	MemoryMB    int64   `json:"memoryMb"`
	// MemorySwapMB nulo = swap desativado (MemorySwap == Memory no Docker).
	MemorySwapMB *int64 `json:"memorySwapMb"`
	// PidsLimit 0 = ilimitado, mesma convenção do Docker.
	PidsLimit int64 `json:"pidsLimit"`
	Enabled   bool  `json:"enabled"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	UpdatedBy string    `json:"updatedBy"`
}

// vcpu é NUMERIC no Postgres e o driver não o entrega direto num
// float64 -- o cast explícito evita ter de o passar por string aqui.
const instanceTypeColumns = `name, display_name, vcpu::float8, memory_mb, memory_swap_mb,
	pids_limit, enabled, created_at, updated_at, updated_by`

func scanInstanceType(row interface{ Scan(...any) error }) (InstanceType, error) {
	var (
		it   InstanceType
		swap sql.NullInt64
	)
	err := row.Scan(
		&it.Name, &it.DisplayName, &it.VCPU, &it.MemoryMB, &swap,
		&it.PidsLimit, &it.Enabled, &it.CreatedAt, &it.UpdatedAt, &it.UpdatedBy,
	)
	if err != nil {
		return InstanceType{}, err
	}
	if swap.Valid {
		v := swap.Int64
		it.MemorySwapMB = &v
	}
	return it, nil
}

// ListInstanceTypes devolve o catálogo inteiro (incluindo os desativados,
// para que a UI de administração os consiga voltar a ligar), ordenado por
// tamanho crescente para que o <select> do portal apareça numa ordem que
// faz sentido a quem escolhe.
func (db *DB) ListInstanceTypes(ctx context.Context) ([]InstanceType, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT `+instanceTypeColumns+`
		FROM service_templates.instance_types ORDER BY vcpu, memory_mb, name`)
	if err != nil {
		return nil, fmt.Errorf("store: listando tipos de instância: %w", err)
	}
	defer rows.Close()

	out := []InstanceType{}
	for rows.Next() {
		it, err := scanInstanceType(rows)
		if err != nil {
			return nil, fmt.Errorf("store: lendo tipo de instância: %w", err)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (db *DB) GetInstanceType(ctx context.Context, name string) (InstanceType, error) {
	row := db.sql.QueryRowContext(ctx, `SELECT `+instanceTypeColumns+`
		FROM service_templates.instance_types WHERE name = $1`, name)
	it, err := scanInstanceType(row)
	if errors.Is(err, sql.ErrNoRows) {
		return InstanceType{}, ErrNotFound
	}
	if err != nil {
		return InstanceType{}, fmt.Errorf("store: lendo tipo de instância %q: %w", name, err)
	}
	return it, nil
}

// UpsertInstanceType cria ou substitui um tipo. Alterar um tipo NÃO
// mexe em containers já a correr: o launcher grava uma cópia dos valores
// aplicados em cada instância, precisamente para que editar o catálogo
// não reescreva o histórico nem a contabilidade de capacidade.
func (db *DB) UpsertInstanceType(ctx context.Context, it InstanceType, updatedBy string) error {
	var swap any
	if it.MemorySwapMB != nil {
		swap = *it.MemorySwapMB
	}
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO service_templates.instance_types (
			name, display_name, vcpu, memory_mb, memory_swap_mb, pids_limit, enabled, updated_by
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (name) DO UPDATE SET
			display_name = EXCLUDED.display_name,
			vcpu = EXCLUDED.vcpu,
			memory_mb = EXCLUDED.memory_mb,
			memory_swap_mb = EXCLUDED.memory_swap_mb,
			pids_limit = EXCLUDED.pids_limit,
			enabled = EXCLUDED.enabled,
			updated_by = EXCLUDED.updated_by,
			updated_at = now()
	`, it.Name, it.DisplayName, it.VCPU, it.MemoryMB, swap, it.PidsLimit, it.Enabled, updatedBy)
	if err != nil {
		return fmt.Errorf("store: gravando tipo de instância %q: %w", it.Name, err)
	}
	return nil
}

// ErrInstanceTypeInUse é devolvido ao tentar apagar um tipo que ainda é o
// default de algum modelo -- apagar deixaria esse modelo a apontar para
// um nome que já não existe, e o lançamento só falharia mais tarde.
var ErrInstanceTypeInUse = errors.New("store: tipo de instância em uso por um ou mais modelos")

func (db *DB) DeleteInstanceType(ctx context.Context, name string) error {
	var inUse bool
	err := db.sql.QueryRowContext(ctx, `SELECT EXISTS(
		SELECT 1 FROM service_templates.templates WHERE default_instance_type = $1)`, name).Scan(&inUse)
	if err != nil {
		return fmt.Errorf("store: verificando uso do tipo %q: %w", name, err)
	}
	if inUse {
		return ErrInstanceTypeInUse
	}

	res, err := db.sql.ExecContext(ctx, `DELETE FROM service_templates.instance_types WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("store: apagando tipo de instância %q: %w", name, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: confirmando remoção do tipo %q: %w", name, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
