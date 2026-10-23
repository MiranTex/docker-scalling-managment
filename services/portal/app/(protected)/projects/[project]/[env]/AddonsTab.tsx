"use client";

import { useEffect, useState } from "react";
import { api, envPath, formatBytes, statusBadge, type Addon, type Backup } from "@/lib/platformClient";
import ConfirmModal from "../../../admin/database/ConfirmModal";

const EMPTY = { slug: "", kind: "postgres", envVar: "", vcpu: "", memoryMb: "" };

export default function AddonsTab({ project, env }: { project: string; env: string }) {
  const [addons, setAddons] = useState<Addon[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [form, setForm] = useState(EMPTY);
  const [saving, setSaving] = useState(false);
  const [backups, setBackups] = useState<Record<string, Backup[]>>({});
  const [deleteTarget, setDeleteTarget] = useState<Addon | null>(null);
  const [purge, setPurge] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<{ addon: string; file: string } | null>(null);

  async function load() {
    try {
      setAddons(await api<Addon[]>(envPath(project, env, "addons")));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [project, env]);

  async function create() {
    setError(null);
    setSaving(true);
    try {
      await api(envPath(project, env, "addons"), {
        method: "POST",
        body: JSON.stringify({
          slug: form.slug.trim(),
          kind: form.kind,
          envVar: form.envVar.trim(),
          vcpu: Number(form.vcpu) || 0,
          memoryMb: Number(form.memoryMb) || 0,
        }),
      });
      setForm(EMPTY);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function toggleBackups(a: Addon) {
    try {
      await api(envPath(project, env, "addons", a.slug), {
        method: "PATCH",
        body: JSON.stringify({ backupEnabled: !a.backupEnabled }),
      });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function backupNow(a: Addon) {
    try {
      await api(envPath(project, env, "addons", a.slug, "backups"), { method: "POST" });
      setNotice(`Backup de ${a.slug} a correr -- acompanha no separador Jobs.`);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function loadBackups(a: Addon) {
    try {
      const list = await api<Backup[]>(envPath(project, env, "addons", a.slug, "backups"));
      setBackups((b) => ({ ...b, [a.slug]: list }));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function restore() {
    if (!restoreTarget) return;
    const target = restoreTarget;
    setRestoreTarget(null);
    try {
      await api(envPath(project, env, "addons", target.addon, "restore"), {
        method: "POST",
        body: JSON.stringify({ file: target.file }),
      });
      setNotice(`Restore de ${target.file} a correr -- acompanha no separador Jobs.`);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function remove() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleteTarget(null);
    try {
      await api(`${envPath(project, env, "addons", target.slug)}${purge ? "?purge=true" : ""}`, { method: "DELETE" });
      setPurge(false);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <>
      <p className="muted">
        Cada addon é um container próprio deste ambiente, com volume próprio. A URL de ligação fica como segredo gerido
        (ex: <code>DATABASE_URL</code>) e é injetada em todos os serviços do ambiente.
      </p>
      {error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}
      {!addons ? (
        <p className="muted">A carregar...</p>
      ) : addons.length === 0 ? (
        <p className="muted">Sem addons.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Addon</th>
              <th>Estado</th>
              <th>Variável</th>
              <th>Recursos</th>
              <th>Backups</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {addons.map((a) => (
              <tr key={a.id}>
                <td>
                  <strong>{a.slug}</strong>
                  <div className="muted">
                    {a.image} · <code>{a.host}</code>
                  </div>
                </td>
                <td>
                  <span className={statusBadge(a.status)}>{a.status}</span>
                </td>
                <td>
                  <code>{a.envVar}</code>
                </td>
                <td>
                  {a.vcpu} vCPU / {a.memoryMb} MB
                </td>
                <td>
                  {a.kind === "postgres" ? (
                    <>
                      <label className="checkbox">
                        <input type="checkbox" checked={a.backupEnabled} onChange={() => toggleBackups(a)} /> diário
                      </label>
                      <div className="muted">
                        último: {a.lastBackupAt ? new Date(a.lastBackupAt).toLocaleString("pt-PT") : "nunca"}
                      </div>
                      <div className="actions">
                        <button className="secondary" onClick={() => backupNow(a)}>
                          Backup agora
                        </button>
                        <button className="secondary" onClick={() => loadBackups(a)}>
                          Ver backups
                        </button>
                      </div>
                      {backups[a.slug] && (
                        <ul className="plain">
                          {backups[a.slug].length === 0 && <li className="muted">nenhum backup</li>}
                          {backups[a.slug].map((b) => (
                            <li key={b.file}>
                              <code>{b.file}</code> ({formatBytes(b.size)}){" "}
                              <button className="danger" onClick={() => setRestoreTarget({ addon: a.slug, file: b.file })}>
                                Repor
                              </button>
                            </li>
                          ))}
                        </ul>
                      )}
                    </>
                  ) : (
                    <span className="muted">AOF no volume</span>
                  )}
                </td>
                <td>
                  <button className="danger" onClick={() => setDeleteTarget(a)}>
                    Apagar
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h2>Novo addon</h2>
      <div className="form-grid">
        <div>
          <label htmlFor="addon-kind">Tipo</label>
          <select id="addon-kind" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
            <option value="postgres">PostgreSQL 17</option>
            <option value="redis">Redis</option>
          </select>
        </div>
        <div>
          <label htmlFor="addon-slug">Slug (nome DNS no ambiente)</label>
          <input
            id="addon-slug"
            value={form.slug}
            onChange={(e) => setForm({ ...form, slug: e.target.value })}
            placeholder={form.kind === "postgres" ? "db" : "cache"}
          />
        </div>
        <div>
          <label htmlFor="addon-envvar">Variável (opcional)</label>
          <input
            id="addon-envvar"
            value={form.envVar}
            onChange={(e) => setForm({ ...form, envVar: e.target.value })}
            placeholder={form.kind === "postgres" ? "DATABASE_URL" : "REDIS_URL"}
          />
        </div>
        <div>
          <label htmlFor="addon-vcpu">vCPU (opcional)</label>
          <input id="addon-vcpu" value={form.vcpu} onChange={(e) => setForm({ ...form, vcpu: e.target.value })} />
        </div>
        <div>
          <label htmlFor="addon-mem">Memória MB (opcional)</label>
          <input id="addon-mem" value={form.memoryMb} onChange={(e) => setForm({ ...form, memoryMb: e.target.value })} />
        </div>
      </div>
      <button className="secondary" disabled={saving || !form.slug.trim()} onClick={create}>
        {saving ? "A criar..." : "Criar addon"}
      </button>

      {deleteTarget && (
        <ConfirmModal
          title="Apagar addon"
          message={`Remove o container de ${deleteTarget.slug} e o segredo ${deleteTarget.envVar}. Os dados ficam no volume, a não ser que marques "apagar dados" antes de confirmar.`}
          expectedText={deleteTarget.slug}
          confirmLabel={purge ? "Apagar com dados" : "Apagar"}
          onConfirm={remove}
          onCancel={() => setDeleteTarget(null)}
        >
          <label className="checkbox">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} /> apagar dados e backups
          </label>
        </ConfirmModal>
      )}
      {restoreTarget && (
        <ConfirmModal
          title="Repor backup"
          message={`Substitui o conteúdo atual de ${restoreTarget.addon} pelo backup ${restoreTarget.file}.`}
          expectedText={restoreTarget.addon}
          confirmLabel="Repor"
          onConfirm={restore}
          onCancel={() => setRestoreTarget(null)}
        />
      )}
    </>
  );
}
