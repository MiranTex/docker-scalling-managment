"use client";

import { useEffect, useState } from "react";
import type { BackupJob, DbStatus, ProvisionedSchema, RestoreJob, SchemaCredentials, StanzaInfo } from "@/lib/dbAdminClient";
import ConfirmModal from "./ConfirmModal";

const BACKUP_TYPES = ["full", "diff", "incr"] as const;
const RESTORE_TYPES = ["time", "xid", "lsn", "name"] as const;

function formatBytes(bytes: number): string {
  if (!bytes) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`;
}

export default function DatabaseAdminClient() {
  const [status, setStatus] = useState<DbStatus | null>(null);
  const [backups, setBackups] = useState<StanzaInfo | null>(null);
  const [job, setJob] = useState<BackupJob | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [triggering, setTriggering] = useState(false);

  const [restoreType, setRestoreType] = useState<(typeof RESTORE_TYPES)[number]>("time");
  const [restoreTarget, setRestoreTarget] = useState("");
  const [restoreJob, setRestoreJob] = useState<RestoreJob | null>(null);
  const [restoreError, setRestoreError] = useState<string | null>(null);
  const [confirmModal, setConfirmModal] = useState<"trigger" | "promote" | null>(null);

  const [schemas, setSchemas] = useState<ProvisionedSchema[]>([]);
  const [schemaName, setSchemaName] = useState("");
  const [roleName, setRoleName] = useState("");
  const [schemaError, setSchemaError] = useState<string | null>(null);
  const [schemaBusy, setSchemaBusy] = useState<string | null>(null);
  const [credentials, setCredentials] = useState<SchemaCredentials | null>(null);
  const [deleteSchema, setDeleteSchema] = useState<ProvisionedSchema | null>(null);

  async function loadStatus() {
    const res = await fetch("/api/admin/database");
    if (res.ok) setStatus(await res.json());
  }

  async function loadBackups() {
    const res = await fetch("/api/admin/database/backups");
    if (res.ok) setBackups(await res.json());
  }

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
    loadStatus();
    loadBackups();
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
      const res = await fetch(`/api/admin/database/schemas/${encodeURIComponent(schema.schema_name)}/reset-password`, { method: "POST" });
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
      await navigator.clipboard.writeText(text);
    } catch {
      // O texto continua visível para seleção manual.
    }
  }

  // Enquanto houver um job de backup a correr, sondamos o estado dele de
  // poucos em poucos segundos -- um backup full pode demorar minutos, não
  // faz sentido o utilizador ficar sem feedback nenhum até lá.
  useEffect(() => {
    if (!job || job.status !== "running") return;
    const interval = setInterval(async () => {
      const res = await fetch(`/api/admin/database/backups/jobs/${job.id}`);
      if (!res.ok) return;
      const updated: BackupJob = await res.json();
      setJob(updated);
      if (updated.status !== "running") {
        await loadBackups();
        await loadStatus();
      }
    }, 3000);
    return () => clearInterval(interval);
  }, [job]);

  // Enquanto o restore está a correr (a parar/restaurar/arrancar o
  // Postgres), sondamos o estado -- assim que chegar a
  // "awaiting_confirmation" paramos de sondar e mostramos o botão de
  // confirmação (não faz sentido continuar a sondar algo que já não
  // muda sozinho, só uma ação humana o desbloqueia a partir daqui).
  useEffect(() => {
    if (!restoreJob || restoreJob.status !== "running") return;
    const interval = setInterval(async () => {
      const res = await fetch(`/api/admin/database/restore/jobs/${restoreJob.id}`);
      if (!res.ok) return;
      const updated: RestoreJob = await res.json();
      setRestoreJob(updated);
    }, 3000);
    return () => clearInterval(interval);
  }, [restoreJob]);

  async function handleTriggerRestore() {
    setConfirmModal(null);
    setRestoreError(null);
    try {
      const res = await fetch("/api/admin/database/restore", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type: restoreType, target: restoreTarget }),
      });
      const data = await res.json();
      if (!res.ok) {
        setRestoreError(data.message ?? "erro ao disparar restore");
        return;
      }
      setRestoreJob(data);
    } catch {
      setRestoreError("erro ao disparar restore");
    }
  }

  async function handleConfirmRestore() {
    if (!restoreJob) return;
    setConfirmModal(null);
    setRestoreError(null);
    try {
      const res = await fetch(`/api/admin/database/restore/jobs/${restoreJob.id}/confirm`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        setRestoreError(data.message ?? "erro ao confirmar restore");
        return;
      }
      setRestoreJob(data);
      await loadStatus();
      await loadBackups();
    } catch {
      setRestoreError("erro ao confirmar restore");
    }
  }

  async function handleTriggerBackup(type: (typeof BACKUP_TYPES)[number]) {
    setError(null);
    setTriggering(true);
    try {
      const res = await fetch("/api/admin/database/backups", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data.message ?? "erro ao disparar backup");
        return;
      }
      setJob(data);
    } finally {
      setTriggering(false);
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Base de dados</h1>
        {error && <div className="error">{error}</div>}
        {!status ? (
          <p className="muted">A carregar estado...</p>
        ) : (
          <table>
            <tbody>
              <tr>
                <th>Base de dados</th>
                <td>{status.postgres.database_name}</td>
              </tr>
              <tr>
                <th>Versão</th>
                <td>{status.postgres.version}</td>
              </tr>
              <tr>
                <th>Tamanho</th>
                <td>{formatBytes(status.postgres.database_size_bytes)}</td>
              </tr>
              <tr>
                <th>Ligações abertas</th>
                <td>{status.postgres.connection_count}</td>
              </tr>
              <tr>
                <th>A correr desde</th>
                <td>{new Date(status.postgres.start_time).toLocaleString("pt-PT")}</td>
              </tr>
              <tr>
                <th>Último backup</th>
                <td>
                  {status.last_backup_at
                    ? new Date(status.last_backup_at).toLocaleString("pt-PT")
                    : "nenhum ainda"}
                </td>
              </tr>
            </tbody>
          </table>
        )}
      </div>

      <div className="card wide">
        <h1>Backups</h1>
        <p className="muted">
          Backups full/incremental já correm sozinhos por agendamento (ver
          services/database/README.md) -- isto é só para disparar um
          backup manual, ex. antes de uma migração arriscada.
        </p>
        <div className="row">
          {BACKUP_TYPES.map((t) => (
            <button
              key={t}
              className="secondary"
              disabled={triggering || job?.status === "running"}
              onClick={() => handleTriggerBackup(t)}
            >
              {triggering ? "A iniciar..." : `Backup ${t}`}
            </button>
          ))}
        </div>

        {job && (
          <p className="muted">
            Job {job.id} ({job.type}): {job.status}
            {job.status === "failed" && job.error ? ` -- ${job.error}` : ""}
          </p>
        )}

        {!backups || backups.backup.length === 0 ? (
          <p className="muted">Nenhum backup ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Label</th>
                <th>Tipo</th>
                <th>Concluído</th>
                <th>Tamanho</th>
              </tr>
            </thead>
            <tbody>
              {[...backups.backup].reverse().map((b) => (
                <tr key={b.label}>
                  <td>{b.label}</td>
                  <td>{b.type}</td>
                  <td>{new Date(b.timestamp.stop * 1000).toLocaleString("pt-PT")}</td>
                  <td>{formatBytes(b.info.size)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card wide">
        <h1>Schemas</h1>
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
        <button
          className="primary"
          disabled={!schemaName || !roleName || schemaBusy !== null}
          onClick={handleCreateSchema}
        >
          {schemaBusy === "create" ? "A criar..." : "Criar schema"}
        </button>

        {credentials && (
          <div className="notice">
            <strong>Guarda estas credenciais agora. Não serão mostradas novamente.</strong>
            <p>DATABASE_URL</p>
            <pre className="generated">{credentials.database_url}</pre>
            <button className="secondary" onClick={() => copyToClipboard(credentials.database_url)}>Copiar URL</button>
            <p>Variáveis PostgreSQL</p>
            <pre className="generated">{credentials.pg_env}</pre>
            <div className="row">
              <button className="secondary" onClick={() => copyToClipboard(credentials.pg_env)}>Copiar variáveis</button>
              <button className="secondary" onClick={() => setCredentials(null)}>Fechar</button>
            </div>
          </div>
        )}

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
                      <button
                        className="danger"
                        disabled={schemaBusy !== null}
                        onClick={() => setDeleteSchema(schema)}
                      >
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

      <div className="card wide">
        <h1>Restaurar para um instante (PITR)</h1>
        <p className="error">
          Isto para o Postgres, sobrescreve os dados com o backup escolhido e reaplica o WAL até ao
          instante indicado -- não há "cancelar" a meio. Usa só se tiveres a certeza.
        </p>
        {restoreError && <div className="error">{restoreError}</div>}

        {!restoreJob && (
          <>
            <label htmlFor="restore-type">Tipo de alvo</label>
            <select id="restore-type" value={restoreType} onChange={(e) => setRestoreType(e.target.value as (typeof RESTORE_TYPES)[number])}>
              {RESTORE_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
            <label htmlFor="restore-target">
              Alvo {restoreType === "time" && "(ex: 2026-08-04 10:00:00)"}
            </label>
            <input
              id="restore-target"
              type="text"
              value={restoreTarget}
              onChange={(e) => setRestoreTarget(e.target.value)}
              placeholder={restoreType === "time" ? "2026-08-04 10:00:00" : ""}
            />
            <button className="danger" disabled={!restoreTarget} onClick={() => setConfirmModal("trigger")}>
              Restaurar
            </button>
          </>
        )}

        {restoreJob && (
          <>
            <table>
              <tbody>
                <tr>
                  <th>Job</th>
                  <td>{restoreJob.id}</td>
                </tr>
                <tr>
                  <th>Alvo</th>
                  <td>
                    {restoreJob.type}={restoreJob.target}
                  </td>
                </tr>
                <tr>
                  <th>Estado</th>
                  <td>{restoreJob.status}</td>
                </tr>
                {restoreJob.error && (
                  <tr>
                    <th>Erro</th>
                    <td className="error">{restoreJob.error}</td>
                  </tr>
                )}
              </tbody>
            </table>

            {restoreJob.status === "awaiting_confirmation" && (
              <>
                <p className="muted">
                  O Postgres está em recovery pausado no instante pedido -- inspeciona os dados (ex.
                  via Adminer) antes de confirmar. Confirmar promove esta timeline (irreversível) e
                  dispara logo a seguir um backup full de reancoragem.
                </p>
                <button className="danger" onClick={() => setConfirmModal("promote")}>
                  Confirmar e promover
                </button>
              </>
            )}

            {(restoreJob.status === "succeeded" || restoreJob.status === "failed") && (
              <button className="secondary" onClick={() => setRestoreJob(null)}>
                Novo restore
              </button>
            )}
          </>
        )}
      </div>

      {confirmModal === "trigger" && status && (
        <ConfirmModal
          title="Confirmar restore"
          message={`Isto vai parar o Postgres e sobrescrever "${status.postgres.database_name}" com o backup mais próximo de ${restoreType}=${restoreTarget}.`}
          expectedText={status.postgres.database_name}
          confirmLabel="Restaurar"
          onConfirm={handleTriggerRestore}
          onCancel={() => setConfirmModal(null)}
        />
      )}

      {confirmModal === "promote" && (
        <ConfirmModal
          title="Confirmar promoção"
          message="Isto promove a timeline restaurada -- a partir daqui não há como voltar atrás sem outro restore."
          expectedText="promover"
          confirmLabel="Confirmar e promover"
          onConfirm={handleConfirmRestore}
          onCancel={() => setConfirmModal(null)}
        />
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
