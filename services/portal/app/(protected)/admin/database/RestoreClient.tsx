"use client";

import { useEffect, useState } from "react";
import type { DbStatus, RestoreJob } from "@/lib/dbAdminClient";
import ConfirmModal from "./ConfirmModal";

const RESTORE_TYPES = ["time", "xid", "lsn", "name"] as const;

export default function RestoreClient() {
  const [status, setStatus] = useState<DbStatus | null>(null);
  const [restoreType, setRestoreType] = useState<(typeof RESTORE_TYPES)[number]>("time");
  const [restoreTarget, setRestoreTarget] = useState("");
  const [restoreJob, setRestoreJob] = useState<RestoreJob | null>(null);
  const [restoreError, setRestoreError] = useState<string | null>(null);
  const [confirmModal, setConfirmModal] = useState<"trigger" | "promote" | null>(null);

  async function loadStatus() {
    const res = await fetch("/api/admin/database");
    if (res.ok) setStatus(await res.json());
  }

  useEffect(() => {
    loadStatus();
  }, []);

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
    } catch {
      setRestoreError("erro ao confirmar restore");
    }
  }

  return (
    <>
      <div className="card wide">
        <h2>Restaurar para um instante (PITR)</h2>
        <p className="error">
          Isto para o Postgres, sobrescreve os dados com o backup escolhido e reaplica o WAL até ao instante indicado --
          não há "cancelar" a meio. Usa só se tiveres a certeza.
        </p>
        {restoreError && <div className="error">{restoreError}</div>}

        {!restoreJob && (
          <>
            <label htmlFor="restore-type">Tipo de alvo</label>
            <select
              id="restore-type"
              value={restoreType}
              onChange={(e) => setRestoreType(e.target.value as (typeof RESTORE_TYPES)[number])}
            >
              {RESTORE_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
            <label htmlFor="restore-target">Alvo {restoreType === "time" && "(ex: 2026-08-04 10:00:00)"}</label>
            <input
              id="restore-target"
              type="text"
              value={restoreTarget}
              onChange={(e) => setRestoreTarget(e.target.value)}
              placeholder={restoreType === "time" ? "2026-08-04 10:00:00" : ""}
            />
            <button className="danger" disabled={!restoreTarget || !status} onClick={() => setConfirmModal("trigger")}>
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
                  O Postgres está em recovery pausado no instante pedido -- inspeciona os dados (ex. via Adminer) antes
                  de confirmar. Confirmar promove esta timeline (irreversível) e dispara logo a seguir um backup full de
                  reancoragem.
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
    </>
  );
}
