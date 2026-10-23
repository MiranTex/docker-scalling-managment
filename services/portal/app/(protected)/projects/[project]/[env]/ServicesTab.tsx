"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { api, envPath, projectPath, splitArgs, statusBadge, type Service, type Template } from "@/lib/platformClient";

const EMPTY = { slug: "", kind: "web", port: "8080", healthPath: "/", cmd: "", image: "", template: "", templateVersion: "" };

export default function ServicesTab({ project, env }: { project: string; env: string }) {
  const [services, setServices] = useState<Service[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState(EMPTY);
  const [saving, setSaving] = useState(false);
  const [open, setOpen] = useState(false);
  // Modelos ativos do projeto, para "criar a partir de um modelo".
  const [templates, setTemplates] = useState<Template[]>([]);
  const [fromTemplate, setFromTemplate] = useState(false);

  async function load() {
    try {
      setServices(await api<Service[]>(envPath(project, env, "services")));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(load, 4000);
    return () => clearInterval(t);
  }, [project, env]);

  useEffect(() => {
    if (!open) return;
    api<Template[]>(projectPath(project, "templates"))
      .then((list) => setTemplates(list.filter((t) => !t.archivedAt)))
      .catch(() => setTemplates([]));
  }, [open, project]);

  const chosen = templates.find((t) => t.slug === form.template);

  async function createFromTemplate() {
    setError(null);
    setSaving(true);
    try {
      // O launcher copia a configuração do modelo e faz logo o primeiro
      // deploy (com a imagem do modelo, ou a indicada aqui).
      await api(envPath(project, env, "services"), {
        method: "POST",
        body: JSON.stringify({
          slug: form.slug.trim(),
          template: form.template,
          templateVersion: Number(form.templateVersion) || 0,
          image: form.image.trim(),
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

  async function create() {
    setError(null);
    setSaving(true);
    try {
      const web = form.kind === "web";
      await api(envPath(project, env, "services"), {
        method: "POST",
        body: JSON.stringify({
          slug: form.slug.trim(),
          kind: form.kind,
          cmd: splitArgs(form.cmd),
          ...(web ? { port: Number(form.port), healthPath: form.healthPath.trim() } : {}),
        }),
      });
      if (form.image.trim()) {
        await api(envPath(project, env, "services", form.slug.trim(), "deployments"), {
          method: "POST",
          body: JSON.stringify({ image: form.image.trim() }),
        });
      }
      setForm(EMPTY);
      setOpen(false);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  function state(s: Service): { label: string; cls: string } {
    if (s.deploying) return { label: "a fazer deploy", cls: statusBadge("deploying") };
    if (s.desiredState === "stopped") return { label: "parado", cls: "badge" };
    if (!s.currentRevision) return { label: "sem deploy", cls: "badge" };
    const target = s.scalingMode === "fixed" ? s.replicas : s.minReplicas;
    return s.readyReplicas >= Math.min(target, 1)
      ? { label: "a correr", cls: statusBadge("running") }
      : { label: "sem réplicas prontas", cls: statusBadge("failed") };
  }

  return (
    <>
      <div className="page-header">
        <p className="muted" style={{ margin: 0 }}>
          Web na porta da tua app, ou um worker de filas.
        </p>
        <button className="primary" onClick={() => setOpen(true)}>
          Novo serviço
        </button>
      </div>
      {!open && error && <div className="error">{error}</div>}
      {!services ? (
        <p className="muted">A carregar...</p>
      ) : services.length === 0 ? (
        <p className="muted">Sem serviços ainda.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Serviço</th>
              <th>Estado</th>
              <th>Revisão</th>
              <th>Réplicas</th>
              <th>Endereço</th>
            </tr>
          </thead>
          <tbody>
            {services.map((s) => {
              const st = state(s);
              return (
                <tr key={s.id}>
                  <td>
                    <Link href={`/projects/${project}/${env}/services/${s.slug}`}>
                      <strong>{s.slug}</strong>
                    </Link>
                    <div className="muted">{s.kind === "web" ? `web :${s.port}` : "worker"}</div>
                  </td>
                  <td>
                    <span className={st.cls}>{st.label}</span>
                    {s.problem && <div className="muted">{s.problem}</div>}
                  </td>
                  <td>
                    {s.currentRevision ? (
                      <>
                        #{s.currentRevision.number} <code className="truncate">{s.currentRevision.image}</code>
                      </>
                    ) : (
                      "—"
                    )}
                  </td>
                  <td>
                    {s.readyReplicas} pronta(s) ·{" "}
                    {s.scalingMode === "fixed" ? `${s.replicas} fixas` : `auto ${s.minReplicas}-${s.maxReplicas}`}
                  </td>
                  <td>
                    {s.urls.map((u) => (
                      <div key={u}>
                        <a href={u} target="_blank" rel="noreferrer">
                          {u.replace(/^https?:\/\//, "")}
                        </a>
                      </div>
                    ))}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      {open && (
        <Modal title="Novo serviço" onClose={() => setOpen(false)}>
          {error && <div className="error">{error}</div>}
          <div className="tabs">
            <button className={!fromTemplate ? "tab active" : "tab"} onClick={() => setFromTemplate(false)}>
              Do zero
            </button>
            <button className={fromTemplate ? "tab active" : "tab"} onClick={() => setFromTemplate(true)}>
              A partir de um modelo
            </button>
          </div>
          {fromTemplate ? (
            <>
              {templates.length === 0 ? (
                <p className="muted">
                  Este projeto ainda não tem modelos -- cria-os em <Link href={`/projects/${project}?tab=templates`}>Modelos</Link>.
                </p>
              ) : (
                <div className="form-grid">
                  <div>
                    <label htmlFor="svc-tslug">Slug</label>
                    <input id="svc-tslug" value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} placeholder="web" />
                  </div>
                  <div>
                    <label htmlFor="svc-template">Modelo</label>
                    <select
                      id="svc-template"
                      value={form.template}
                      onChange={(e) => setForm({ ...form, template: e.target.value, templateVersion: "" })}
                    >
                      <option value="">escolhe...</option>
                      {templates.map((t) => (
                        <option key={t.id} value={t.slug}>
                          {t.name} ({t.slug})
                        </option>
                      ))}
                    </select>
                  </div>
                  {chosen && (
                    <div>
                      <label htmlFor="svc-tversion">Versão</label>
                      <select
                        id="svc-tversion"
                        value={form.templateVersion}
                        onChange={(e) => setForm({ ...form, templateVersion: e.target.value })}
                      >
                        <option value="">mais recente (v{chosen.latestVersion})</option>
                        {Array.from({ length: chosen.latestVersion }, (_, i) => chosen.latestVersion - i).map((n) => (
                          <option key={n} value={n}>
                            v{n}
                          </option>
                        ))}
                      </select>
                    </div>
                  )}
                  <div>
                    <label htmlFor="svc-timage">Imagem (vazio = a do modelo)</label>
                    <input
                      id="svc-timage"
                      value={form.image}
                      onChange={(e) => setForm({ ...form, image: e.target.value })}
                      placeholder="ghcr.io/eu/app:1.0.0"
                    />
                  </div>
                </div>
              )}
              <p className="muted">O serviço fica com a configuração do modelo e arranca logo com o primeiro deploy.</p>
              <div className="row">
                <button className="secondary" onClick={() => setOpen(false)}>
                  Cancelar
                </button>
                <button className="primary" disabled={saving || !form.slug.trim() || !form.template} onClick={createFromTemplate}>
                  {saving ? "A criar..." : "Criar e fazer deploy"}
                </button>
              </div>
            </>
          ) : (
            <>
      <div className="form-grid">
        <div>
          <label htmlFor="svc-slug">Slug</label>
          <input id="svc-slug" value={form.slug} onChange={(e) => setForm({ ...form, slug: e.target.value })} placeholder="web" />
        </div>
        <div>
          <label htmlFor="svc-kind">Tipo</label>
          <select id="svc-kind" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
            <option value="web">web (recebe tráfego HTTP)</option>
            <option value="worker">worker (filas, cron, sem tráfego)</option>
          </select>
        </div>
        {form.kind === "web" && (
          <>
            <div>
              <label htmlFor="svc-port">Porta da aplicação</label>
              <input id="svc-port" value={form.port} onChange={(e) => setForm({ ...form, port: e.target.value })} />
            </div>
            <div>
              <label htmlFor="svc-health">Health check (vazio = só TCP)</label>
              <input
                id="svc-health"
                value={form.healthPath}
                onChange={(e) => setForm({ ...form, healthPath: e.target.value })}
                placeholder="/healthz"
              />
            </div>
          </>
        )}
        <div>
          <label htmlFor="svc-cmd">Comando (vazio = o da imagem)</label>
          <input
            id="svc-cmd"
            value={form.cmd}
            onChange={(e) => setForm({ ...form, cmd: e.target.value })}
            placeholder={form.kind === "worker" ? "php artisan queue:work" : ""}
          />
        </div>
        <div>
          <label htmlFor="svc-image">Imagem para o primeiro deploy (opcional)</label>
          <input
            id="svc-image"
            value={form.image}
            onChange={(e) => setForm({ ...form, image: e.target.value })}
            placeholder="ghcr.io/eu/app:1.0.0"
          />
        </div>
      </div>
      <button className="primary" disabled={saving || !form.slug.trim()} onClick={create}>
        {saving ? "A criar..." : "Criar serviço"}
      </button>
          <button className="secondary" onClick={() => setOpen(false)}>
            Cancelar
          </button>
            </>
          )}
        </Modal>
      )}
    </>
  );
}
