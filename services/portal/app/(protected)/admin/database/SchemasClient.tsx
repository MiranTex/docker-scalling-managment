"use client";

import { copyText } from "@/lib/clipboard";
import { useEffect, useState } from "react";
import type { ProvisionedSchema, SchemaCredentials } from "@/lib/dbAdminClient";
import Modal from "@/components/Modal";
import ConfirmModal from "./ConfirmModal";

export default function SchemasClient() {
  const [schemas, setSchemas] = useState<ProvisionedSchema[]>([]);
  const [createOpen, setCreateOpen] = useState(false);
  const [schemaName, setSchemaName] = useState("");
  const [roleName, setRoleName] = useState("");
  const [schemaError, setSchemaError] = useState<string | null>(null);
  const [schemaBusy, setSchemaBusy] = useState<string | null>(null);
  const [credentials, setCredentials] = useState<SchemaCredentials | null>(null);
  const [deleteSchema, setDeleteSchema] = useState<ProvisionedSchema | null>(null);

  async function loadSchemas() {
    const res = await fetch("/api/admin/database/schemas");
    const data = await res.json().catch(() => []);
    if (res.ok) {
      setSchemas(data);
    } else {
      setSchemaError(data.message ?? "erro ao listar schemas");
    }
  }

  useEffect(() => {
    loadSchemas();
  }, []);

  async function handleCreateSchema() {
    setSchemaError(null);
    setCredentials(null);
    setSchemaBusy("create");
    try {
      const res = await fetch("/api/admin/database/schemas", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ schema_name: schemaName, role_name: roleName }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setSchemaError(data.message ?? "erro ao criar schema");
        return;
      }
      setCredentials(data);
      setCreateOpen(false);
      setSchemaName("");
      setRoleName("");
      await loadSchemas();
    } finally {
      setSchemaBusy(null);
    }
  }

  async function handleResetPassword(schema: ProvisionedSchema) {
    setSchemaError(null);
    setCredentials(null);
    setSchemaBusy(`reset:${schema.schema_name}`);
    try {
      const res = await fetch(`/api/admin/database/schemas/${encodeURIComponent(schema.schema_name)}/reset-password`, {
        method: "POST",
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setSchemaError(data.message ?? "erro ao redefinir senha");
        return;
      }
      setCredentials(data);
    } finally {
      setSchemaBusy(null);
    }
  }

  async function handleDeleteSchema() {
    if (!deleteSchema) return;
    const name = deleteSchema.schema_name;
    setDeleteSchema(null);
    setSchemaError(null);
    setCredentials(null);
    setSchemaBusy(`delete:${name}`);
    try {
      const res = await fetch(`/api/admin/database/schemas/${encodeURIComponent(name)}`, {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ confirmation: name }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setSchemaError(data.message ?? "erro ao eliminar schema");
        return;
      }
      await loadSchemas();
    } finally {
      setSchemaBusy(null);
    }
  }

  async function copyToClipboard(text: string) {
    try {
      await copyText(text);
    } catch {
      // O texto continua visível para seleção manual.
    }
  }

  return (
    <>
        <div className="card wide">
        <div className="page-header">
            <h2 style={{ margin: 0 }}>Schemas existentes</h2>
            <button className="primary" onClick={() => { setSchemaError(null); setCreateOpen(true); }}>
              Novo schema
            </button>
          </div>
          {schemaError && <div className="error">{schemaError}</div>}
          {schemas.length === 0 ? (
            <p className="muted">Nenhum schema criado pela interface.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Schema</th>
                  <th>Utilizador</th>
                  <th>Criado</th>
                  <th>Ações</th>
                </tr>
              </thead>
              <tbody>
                {schemas.map((schema) => (
                  <tr key={schema.schema_name}>
                    <td>{schema.schema_name}</td>
                    <td>{schema.role_name}</td>
                    <td>{new Date(schema.created_at).toLocaleString("pt-PT")}</td>
                    <td>
                      <div className="row">
                        <button
                          className="secondary"
                          disabled={schemaBusy !== null}
                          onClick={() => handleResetPassword(schema)}
                        >
                          {schemaBusy === `reset:${schema.schema_name}` ? "A redefinir..." : "Nova senha"}
                        </button>
                        <button className="danger" disabled={schemaBusy !== null} onClick={() => setDeleteSchema(schema)}>
                          Eliminar
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>

      {createOpen && (
        <Modal title="Novo schema" onClose={() => setCreateOpen(false)}>
          <p className="muted">Cria um schema isolado e um utilizador PostgreSQL dedicado.</p>
          {schemaError && <div className="error">{schemaError}</div>}
          <label htmlFor="schema-name">Nome do schema</label>
          <input
            id="schema-name"
            type="text"
            value={schemaName}
            onChange={(event) => setSchemaName(event.target.value)}
            placeholder="minha_app"
          />
          <label htmlFor="role-name">Utilizador PostgreSQL</label>
          <input
            id="role-name"
            type="text"
            value={roleName}
            onChange={(event) => setRoleName(event.target.value)}
            placeholder="minha_app_user"
          />
          <button className="primary" disabled={!schemaName || !roleName || schemaBusy !== null} onClick={handleCreateSchema}>
            {schemaBusy === "create" ? "A criar..." : "Criar schema"}
          </button>

        </Modal>
      )}

      {credentials && (
        <Modal title="Credenciais do schema" onClose={() => setCredentials(null)}>
            <div className="notice">
              <strong>Guarda estas credenciais agora. Não serão mostradas novamente.</strong>
              <p>DATABASE_URL</p>
              <pre className="generated">{credentials.database_url}</pre>
              <button className="secondary" onClick={() => copyToClipboard(credentials.database_url)}>
                Copiar URL
              </button>
              <p>Variáveis PostgreSQL</p>
              <pre className="generated">{credentials.pg_env}</pre>
              <div className="row">
                <button className="secondary" onClick={() => copyToClipboard(credentials.pg_env)}>
                  Copiar variáveis
                </button>
                <button className="secondary" onClick={() => setCredentials(null)}>
                  Fechar
                </button>
              </div>
            </div>
        </Modal>
      )}

      {deleteSchema && (
        <ConfirmModal
          title="Eliminar schema"
          message={`Isto elimina permanentemente o schema "${deleteSchema.schema_name}", todos os seus dados e o utilizador "${deleteSchema.role_name}".`}
          expectedText={deleteSchema.schema_name}
          confirmLabel="Eliminar"
          onConfirm={handleDeleteSchema}
          onCancel={() => setDeleteSchema(null)}
        />
      )}
    </>
  );
}
