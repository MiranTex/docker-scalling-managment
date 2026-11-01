"use client";

import Loading from "@/components/Skeleton";
import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import TerminalModal from "@/components/TerminalModal";
import CopyCommand from "@/components/CopyCommand";
import { addonStatus, api, envPath, formatBytes, setAddonState, type Addon, type Backup } from "@/lib/platformClient";
import ConfirmModal from "../../../admin/database/ConfirmModal";
import Link from "next/link";
import { useCan } from "@/lib/projectRole";

const EMPTY = { slug: "", kind: "postgres", envVar: "", vcpu: "", memoryMb: "" };

// O que cada tipo sugere no formulário e o que o terminal tem à mão.
const KINDS: Record<string, { slug: string; envVar: string; shell: string }> = {
  postgres: { slug: "db", envVar: "DATABASE_URL", shell: "psql disponível" },
  redis: { slug: "cache", envVar: "REDIS_URL", shell: "redis-cli disponível" },
  storage: { slug: "files", envVar: "STORAGE", shell: "weed shell disponível" },
};

export default function AddonsTab({ project, env }: { project: string; env: string }) {
  const canEdit = useCan("developer");
  const [addons, setAddons] = useState<Addon[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [form, setForm] = useState(EMPTY);
  const [saving, setSaving] = useState(false);
  const [open, setOpen] = useState(false);
  const [backups, setBackups] = useState<Record<string, Backup[]>>({});
  const [deleteTarget, setDeleteTarget] = useState<Addon | null>(null);
  const [purge, setPurge] = useState(false);
  const [restoreTarget, setRestoreTarget] = useState<{ addon: string; file: string } | null>(null);
  const [terminal, setTerminal] = useState<Addon | null>(null);

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
      setOpen(false);
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

  async function setState(a: Addon, state: "stop" | "start") {
    setError(null);
    setNotice(null);
    try {
      if (!(await setAddonState(project, env, a, state))) return;
      setNotice(state === "stop" ? `${a.slug} parado.` : `${a.slug} a arrancar.`);
      await load();
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
      <div className="page-header">
      <p className="muted" style={{ margin: 0 }}>
        Cada addon é um container próprio deste ambiente, com volume próprio. A ligação fica em segredos geridos (ex:{" "}
        <code>DATABASE_URL</code>) injetados em todos os serviços do ambiente.
      </p>
        {canEdit && (
        <button className="primary" onClick={() => setOpen(true)}>
          Novo addon
        </button>
        )}
      </div>
      {!open && error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}
      {!addons ? (
        <Loading />
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
                  {/* Os storages têm página própria (buckets, ficheiros, acesso). */}
                  {a.kind === "storage" ? (
                    <Link href={`/projects/${project}/${env}/addons/${a.slug}`}>
                      <strong>{a.slug}</strong>
                    </Link>
                  ) : (
                    <strong>{a.slug}</strong>
                  )}
                  <div className="muted">
                    {a.image} · <code>{a.host}</code>
                  </div>
                  {a.publicUrl && (
                    <div className="muted">
                      público: <code>{a.publicUrl}</code>
                    </div>
                  )}
                </td>
                <td>
                  <span className={addonStatus(a).className}>{addonStatus(a).label}</span>
                </td>
                <td>
                  {(a.envVars ?? [a.envVar]).map((v) => (
                    <div key={v}>
                      <code>{v}</code>
                    </div>
                  ))}
                </td>
                <td>
                  {a.vcpu} vCPU / {a.memoryMb} MB
                </td>
                <td>
                  {a.kind === "postgres" ? (
                    <>
                      <label className="checkbox">
                        <input type="checkbox" checked={a.backupEnabled} disabled={!canEdit} onChange={() => toggleBackups(a)} /> diário
                      </label>
                      <div className="muted">
                        último: {a.lastBackupAt ? new Date(a.lastBackupAt).toLocaleString("pt-PT") : "nunca"}
                      </div>
                      <div className="actions">
                        {canEdit && (
                          <button className="secondary" onClick={() => backupNow(a)}>
                            Backup agora
                          </button>
                        )}
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
                              {canEdit && (
                                <button className="danger" onClick={() => setRestoreTarget({ addon: a.slug, file: b.file })}>
                                  Repor
                                </button>
                              )}
                            </li>
                          ))}
                        </ul>
                      )}
                    </>
                  ) : a.kind === "redis" ? (
                    <span className="muted">AOF no volume</span>
                  ) : (
                    <span className="muted">ficheiros no volume</span>
                  )}
                </td>
                <td>
                  {canEdit && (
                  <div className="actions">
                    {a.desiredState !== "stopped" && (
                      <button className="secondary" onClick={() => setTerminal(a)} title={`Shell no container (${KINDS[a.kind]?.shell ?? "sh"})`}>
                        Terminal
                      </button>
                    )}
                    {a.desiredState === "stopped" ? (
                      <button className="secondary" onClick={() => setState(a, "start")}>
                        Arrancar
                      </button>
                    ) : (
                      <button className="secondary" onClick={() => setState(a, "stop")}>
                        Parar
                      </button>
                    )}
                    <button className="danger" onClick={() => setDeleteTarget(a)}>
                      Apagar
                    </button>
                  </div>
                  )}
                  {canEdit && (
                    <div
                      title={
                        a.kind === "storage"
                          ? "Abre o armazenamento na tua máquina (aws cli, rclone, Cyberduck...) com a CLI bs -- ver services/cli/README.md"
                          : "Abre a base de dados na tua máquina (DBeaver, psql...) com a CLI bs -- ver services/cli/README.md"
                      }
                    >
                      <p className="muted" style={{ margin: "8px 0 4px" }}>
                        Da tua máquina:
                      </p>
                      <CopyCommand command={`bs port-forward ${project}/${env}/${a.slug}`} />
                    </div>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {open && (
        <Modal title="Novo addon" onClose={() => setOpen(false)}>
          {error && <div className="error">{error}</div>}
      <div className="form-grid">
        <div>
          <label htmlFor="addon-kind">Tipo</label>
          <select id="addon-kind" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
            <option value="postgres">PostgreSQL 17</option>
            <option value="redis">Redis</option>
            <option value="storage">Armazenamento de ficheiros (API do S3)</option>
          </select>
        </div>
        <div>
          <label htmlFor="addon-slug">Slug (nome DNS no ambiente)</label>
          <input
            id="addon-slug"
            value={form.slug}
            onChange={(e) => setForm({ ...form, slug: e.target.value })}
            placeholder={KINDS[form.kind].slug}
          />
        </div>
        <div>
          <label htmlFor="addon-envvar">{form.kind === "storage" ? "Prefixo das variáveis (opcional)" : "Variável (opcional)"}</label>
          <input
            id="addon-envvar"
            value={form.envVar}
            onChange={(e) => setForm({ ...form, envVar: e.target.value })}
            placeholder={KINDS[form.kind].envVar}
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
      {form.kind === "storage" && (
        <p className="muted">
          Um bucket (<code>app</code>) compatível com os SDKs do S3. Os serviços recebem{" "}
          <code>{(form.envVar.trim() || "STORAGE") + "_ENDPOINT"}</code>, <code>_BUCKET</code>, <code>_REGION</code>,{" "}
          <code>_ACCESS_KEY_ID</code>, <code>_SECRET_ACCESS_KEY</code> e <code>_URL</code>. Só é acessível dentro do
          ambiente.
        </p>
      )}
      <button className="primary" disabled={saving || !form.slug.trim()} onClick={create}>
        {saving ? "A criar..." : "Criar addon"}
      </button>
          <button className="secondary" onClick={() => setOpen(false)}>
            Cancelar
          </button>
        </Modal>
      )}

      {terminal && (
        <TerminalModal
          title={`Terminal · ${terminal.slug}`}
          path={envPath(project, env, "addons", terminal.slug, "terminal")}
          onClose={() => setTerminal(null)}
        />
      )}

      {deleteTarget && (
        <ConfirmModal
          title="Apagar addon"
          message={`Remove o container de ${deleteTarget.slug} e ${(deleteTarget.envVars ?? []).length > 1 ? `os segredos ${deleteTarget.envVar}_*` : `o segredo ${deleteTarget.envVar}`}. Os dados ficam no volume, a não ser que marques "apagar dados" antes de confirmar.`}
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
