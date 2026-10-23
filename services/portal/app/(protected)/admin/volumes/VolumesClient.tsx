"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import Modal from "@/components/Modal";
import PageHeader from "@/components/PageHeader";
import { api, formatBytes, type VolumeView } from "@/lib/platformClient";

const STATUS: Record<VolumeView["status"], { label: string; cls: string }> = {
  "in-use": { label: "em uso", cls: "badge active" },
  free: { label: "livre", cls: "badge" },
  orphan: { label: "órfão", cls: "badge pending" },
};

type StatusFilter = "all" | VolumeView["status"];

const size = (v: VolumeView) => (v.sizeBytes >= 0 ? formatBytes(v.sizeBytes) : "?");
const total = (list: VolumeView[]) => list.reduce((n, v) => n + Math.max(v.sizeBytes, 0), 0);

export default function VolumesClient() {
  const [volumes, setVolumes] = useState<VolumeView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [project, setProject] = useState("");
  const [status, setStatus] = useState<StatusFilter>("all");
  const [toDelete, setToDelete] = useState<VolumeView[] | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [typed, setTyped] = useState("");

  async function load() {
    try {
      setVolumes(await api<VolumeView[]>("volumes"));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, []);

  const projects = useMemo(() => [...new Set((volumes ?? []).map((v) => v.project))].sort(), [volumes]);
  const byProject = (volumes ?? []).filter((v) => !project || v.project === project);
  const shown = byProject.filter((v) => status === "all" || v.status === status);
  const groups = useMemo(() => {
    const m = new Map<string, VolumeView[]>();
    for (const v of shown) m.set(v.project, [...(m.get(v.project) ?? []), v]);
    return [...m.entries()];
  }, [shown]);
  // "Apagar órfãos" respeita o filtro de projeto: só apaga o que estás a ver.
  const orphans = byProject.filter((v) => v.status === "orphan" && v.deletable);

  function openDelete(list: VolumeView[]) {
    setTyped("");
    setToDelete(list);
  }

  async function confirmDelete() {
    if (!toDelete) return;
    setDeleting(true);
    setError(null);
    const failed: string[] = [];
    for (const v of toDelete) {
      try {
        await api(`volumes/${encodeURIComponent(v.name)}`, { method: "DELETE" });
      } catch (e) {
        failed.push(`${v.name}: ${(e as Error).message}`);
      }
    }
    if (failed.length) setError(failed.join("\n"));
    setDeleting(false);
    setToDelete(null);
    await load();
  }

  const expected = toDelete?.length === 1 ? toDelete[0].name : "apagar";

  return (
    <>
      <PageHeader
        title="Volumes"
        description="Os volumes de dados dos projetos: quanto ocupam, a quem pertencem e quem os usa. Um volume órfão ficou de um addon ou ambiente apagado sem apagar os dados."
        actions={
          <>
            <button className="secondary" onClick={load}>
              Atualizar
            </button>
            {orphans.length > 0 && (
              <button className="danger" onClick={() => openDelete(orphans)}>
                Apagar órfãos ({orphans.length} · {formatBytes(total(orphans))})
              </button>
            )}
          </>
        }
      />

      <div className="card full">
        {error && (
          <div className="error" style={{ whiteSpace: "pre-wrap" }}>
            {error}
          </div>
        )}
        <div className="form-grid">
          <div>
            <label htmlFor="vol-project">Projeto</label>
            <select id="vol-project" value={project} onChange={(e) => setProject(e.target.value)}>
              <option value="">todos</option>
              {projects.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="tabs">
          {(["all", "in-use", "free", "orphan"] as StatusFilter[]).map((f) => (
            <button key={f} className={f === status ? "tab active" : "tab"} onClick={() => setStatus(f)}>
              {f === "all" ? "Todos" : STATUS[f].label} ({byProject.filter((v) => f === "all" || v.status === f).length})
            </button>
          ))}
        </div>

        {!volumes ? (
          <p className="muted">A calcular tamanhos...</p>
        ) : groups.length === 0 ? (
          <p className="muted">Nenhum volume.</p>
        ) : (
          groups.map(([proj, list]) => (
            <div key={proj}>
              <h2>
                <Link href={`/projects/${proj}`}>{proj}</Link>{" "}
                <span className="muted">
                  · {list.length} volume(s) · {formatBytes(total(list))}
                </span>
              </h2>
              <table>
                <thead>
                  <tr>
                    <th>Volume</th>
                    <th>Estado</th>
                    <th>Ambiente / addon</th>
                    <th>Usado por</th>
                    <th>Tamanho</th>
                    <th>Criado</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {list.map((v) => (
                    <tr key={v.name}>
                      <td>
                        <code>{v.name}</code>
                      </td>
                      <td>
                        <span className={STATUS[v.status].cls}>{STATUS[v.status].label}</span>
                      </td>
                      <td>
                        {v.environment ? (
                          <Link href={`/projects/${v.project}/${v.environment}`}>{v.environment}</Link>
                        ) : (
                          "—"
                        )}
                        {(v.addon || v.kind) && (
                          <div className="muted">
                            {v.addon ? `addon ${v.addon}` : "addon apagado"}
                            {v.kind && ` · ${v.kind === "data" ? "dados" : "backups"}`}
                          </div>
                        )}
                      </td>
                      <td>
                        {v.usedBy.length === 0
                          ? "—"
                          : v.usedBy.map((u) => (
                              <div key={u.container}>
                                <code>{u.container}</code>{" "}
                                <span className="muted">
                                  ({u.state}) {u.path}
                                </span>
                              </div>
                            ))}
                      </td>
                      <td>{size(v)}</td>
                      <td>{new Date(v.createdAt).toLocaleString("pt-PT")}</td>
                      <td>
                        {v.deletable ? (
                          <button className="danger" onClick={() => openDelete([v])}>
                            Apagar
                          </button>
                        ) : (
                          <span className="muted" title={v.reason}>
                            protegido
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ))
        )}
      </div>

      {toDelete && (
        <Modal
          title={toDelete.length === 1 ? "Apagar volume" : `Apagar ${toDelete.length} volumes órfãos`}
          onClose={() => setToDelete(null)}
        >
          <p className="error">
            Os dados são apagados de forma permanente ({formatBytes(total(toDelete))}). Não há forma de os recuperar.
          </p>
          {toDelete.length > 1 && (
            <ul className="plain">
              {toDelete.map((v) => (
                <li key={v.name}>
                  <code>{v.name}</code> ({size(v)})
                </li>
              ))}
            </ul>
          )}
          <p className="muted">
            Escreve <strong>{expected}</strong> para confirmar.
          </p>
          <input value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus />
          <div className="row">
            <button className="secondary" onClick={() => setToDelete(null)}>
              Cancelar
            </button>
            <button className="danger" disabled={typed !== expected || deleting} onClick={confirmDelete}>
              {deleting ? "A apagar..." : "Apagar"}
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
