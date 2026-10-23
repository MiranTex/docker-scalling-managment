"use client";

import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { api, envPath, statusBadge, type Job } from "@/lib/platformClient";

export default function JobsTab({ project, env }: { project: string; env: string }) {
  const [jobs, setJobs] = useState<Job[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<Job | null>(null);

  async function load() {
    try {
      setJobs(await api<Job[]>(envPath(project, env, "jobs")));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function cancel(id: string) {
    if (!confirm("Cancelar este job? O container é parado de imediato.")) return;
    try {
      await api(envPath(project, env, "jobs", id, "cancel"), { method: "POST" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function show(id: string) {
    try {
      setOpen(await api<Job>(envPath(project, env, "jobs", id)));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(() => {
      load();
      if (open?.status === "running") show(open.id);
    }, 3000);
    return () => clearInterval(t);
  }, [project, env, open?.id, open?.status]);

  return (
    <>
      <p className="muted">Backups, restores, migrations e comandos pontuais deste ambiente (os 50 mais recentes).</p>
      {error && <div className="error">{error}</div>}
      {!jobs ? (
        <p className="muted">A carregar...</p>
      ) : jobs.length === 0 ? (
        <p className="muted">Nenhum job ainda.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Tipo</th>
              <th>Estado</th>
              <th>Comando</th>
              <th>Início</th>
              <th>Por</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {jobs.map((j) => (
              <tr key={j.id}>
                <td>{j.kind}</td>
                <td>
                  <span className={statusBadge(j.status)}>{j.status}</span>
                  {j.exitCode !== undefined && j.exitCode !== null && <span className="muted"> exit {j.exitCode}</span>}
                </td>
                <td>
                  <code className="truncate">{j.cmd.join(" ")}</code>
                </td>
                <td>{new Date(j.createdAt).toLocaleString("pt-PT")}</td>
                <td>{j.createdBy}</td>
                <td>
                  <div className="actions">
                    <button className="secondary" onClick={() => show(j.id)}>
                      Output
                    </button>
                    {j.status === "running" && (
                      <button className="danger" onClick={() => cancel(j.id)}>
                        Cancelar
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {open && (
        <Modal
          title={`Job ${open.kind} · ${open.status}`}
          onClose={() => setOpen(null)}
          width={900}
        >
          <pre className="generated">{open.output || "(sem output ainda)"}</pre>
          <button className="secondary" onClick={() => setOpen(null)}>
            Fechar
          </button>
        </Modal>
      )}
    </>
  );
}
