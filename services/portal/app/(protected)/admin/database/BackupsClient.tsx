"use client";

import { useEffect, useState } from "react";
import type { BackupJob, StanzaInfo } from "@/lib/dbAdminClient";
import { formatBytes } from "./format";

const BACKUP_TYPES = ["full", "diff", "incr"] as const;

export default function BackupsClient() {
  const [backups, setBackups] = useState<StanzaInfo | null>(null);
  const [job, setJob] = useState<BackupJob | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [triggering, setTriggering] = useState(false);

  async function loadBackups() {
    const res = await fetch("/api/admin/database/backups");
    if (res.ok) setBackups(await res.json());
  }

  useEffect(() => {
    loadBackups();
  }, []);

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
      if (updated.status !== "running") await loadBackups();
    }, 3000);
    return () => clearInterval(interval);
  }, [job]);

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
    <div className="split">
      <div className="card wide">
        <h2>Backup manual</h2>
        <p className="muted">
          Backups full/incremental já correm sozinhos por agendamento (ver services/database/README.md) -- isto é só
          para disparar um backup manual, ex. antes de uma migração arriscada.
        </p>
        {error && <div className="error">{error}</div>}
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
      </div>

      <div className="card wide">
        <h2>Histórico</h2>
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
    </div>
  );
}
