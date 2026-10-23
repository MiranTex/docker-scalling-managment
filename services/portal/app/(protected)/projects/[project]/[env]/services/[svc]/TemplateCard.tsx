"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { useCan } from "@/lib/projectRole";
import {
  api,
  formatSpecValue,
  projectPath,
  servicePath,
  SPEC_LABELS,
  type Service,
  type Template,
  type TemplateUpdate,
} from "@/lib/platformClient";

export default function TemplateCard({
  project,
  env,
  service,
  onChanged,
}: {
  project: string;
  env: string;
  service: Service;
  onChanged: () => void;
}) {
  const canEdit = useCan("developer");
  const ref = service.template;
  const [update, setUpdate] = useState<TemplateUpdate | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [applying, setApplying] = useState(false);
  const [saveOpen, setSaveOpen] = useState(false);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [save, setSave] = useState({ target: "", slug: "", name: "", notes: "" });
  const path = (...rest: string[]) => servicePath(project, env, service.slug, ...rest);
  const hasUpdate = ref && !ref.archived && ref.latestVersion > ref.version;

  useEffect(() => {
    if (!saveOpen) return;
    api<Template[]>(projectPath(project, "templates"))
      .then((l) => setTemplates(l.filter((t) => !t.archivedAt)))
      .catch(() => setTemplates([]));
  }, [saveOpen, project]);

  async function showDiff() {
    setError(null);
    try {
      setUpdate(await api<TemplateUpdate>(path("template-update")));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function apply() {
    if (!update) return;
    setApplying(true);
    setError(null);
    try {
      await api(path("template-update"), { method: "POST", body: JSON.stringify({ to: update.toVersion }) });
      setNotice(`Atualizado para v${update.toVersion}. O scaling já se aplica; o resto entra no próximo deploy.`);
      setUpdate(null);
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setApplying(false);
    }
  }

  async function saveAsTemplate() {
    setError(null);
    const template = save.target || save.slug.trim();
    try {
      await api(path("save-as-template"), {
        method: "POST",
        body: JSON.stringify({ template, name: save.name.trim(), notes: save.notes.trim() }),
      });
      setNotice(save.target ? `Nova versão de ${template} criada a partir deste serviço.` : `Modelo ${template} criado (v1).`);
      setSaveOpen(false);
      setSave({ target: "", slug: "", name: "", notes: "" });
      onChanged();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <div className="card full">
      {!update && !saveOpen && error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}
      <div className="row">
        <p style={{ margin: 0 }}>
          {ref ? (
            <>
              Modelo{" "}
              <Link href={`/projects/${project}/templates/${ref.slug}`}>
                <strong>{ref.name}</strong>
              </Link>{" "}
              <code>
                {ref.slug}@v{ref.version}
              </code>
              {ref.archived && <span className="badge"> arquivado</span>}
            </>
          ) : (
            <span className="muted">Criado sem modelo.</span>
          )}
        </p>
        <div className="actions">
          {hasUpdate && canEdit && (
            <button className="primary" onClick={showDiff}>
              v{ref!.latestVersion} disponível · ver diferenças
            </button>
          )}
          <button className="secondary" onClick={() => setSaveOpen(true)} disabled={!service.currentRevision || !canEdit}>
            Guardar como modelo
          </button>
        </div>
      </div>

      {update && (
        <Modal title={`${update.template}: v${update.fromVersion} → v${update.toVersion}`} onClose={() => setUpdate(null)} width={820}>
          {error && <div className="error">{error}</div>}
          {update.notes.length > 0 && (
            <ul className="plain">
              {update.notes.map((n) => (
                <li key={n.version}>
                  <strong>v{n.version}</strong>: {n.notes}
                </li>
              ))}
            </ul>
          )}
          {update.changes.length === 0 ? (
            <p className="muted">O modelo não mudou nenhum campo da configuração (só a imagem do primeiro deploy, que não se aplica a serviços existentes).</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Campo</th>
                  <th>
                    No modelo (v{update.fromVersion} → v{update.toVersion})
                  </th>
                  <th>Agora no serviço</th>
                </tr>
              </thead>
              <tbody>
                {update.changes.map((c) => (
                  <tr key={c.field}>
                    <td>{SPEC_LABELS[c.field] ?? c.field}</td>
                    <td style={{ whiteSpace: "pre-wrap" }}>
                      <code>{formatSpecValue(c.from)}</code> → <code>{formatSpecValue(c.to)}</code>
                    </td>
                    <td style={{ whiteSpace: "pre-wrap" }}>
                      <code>{formatSpecValue(c.current)}</code>
                      {c.overrides && <div className="error">a tua alteração neste serviço vai ser substituída</div>}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <p className="muted">
            Só estes campos mudam; o resto da configuração (incluindo o que alteraste à mão) fica. A imagem que está a
            correr não é tocada.
          </p>
          <div className="row">
            <button className="secondary" onClick={() => setUpdate(null)}>
              Cancelar
            </button>
            <button className="primary" disabled={applying} onClick={apply}>
              {applying ? "A aplicar..." : `Aplicar v${update.toVersion}`}
            </button>
          </div>
        </Modal>
      )}

      {saveOpen && (
        <Modal title="Guardar como modelo" onClose={() => setSaveOpen(false)}>
          {error && <div className="error">{error}</div>}
          <p className="muted">
            Guarda a configuração atual deste serviço, com a imagem que está a correr (
            <code>{service.currentRevision?.image}</code>) como imagem do primeiro deploy.
          </p>
          <label htmlFor="save-target">Onde</label>
          <select id="save-target" value={save.target} onChange={(e) => setSave({ ...save, target: e.target.value })}>
            <option value="">num modelo novo</option>
            {templates.map((t) => (
              <option key={t.id} value={t.slug}>
                versão nova de {t.name} (v{t.latestVersion + 1})
              </option>
            ))}
          </select>
          {!save.target && (
            <div className="form-grid">
              <div>
                <label htmlFor="save-slug">Slug</label>
                <input id="save-slug" value={save.slug} onChange={(e) => setSave({ ...save, slug: e.target.value })} placeholder="laravel-web" />
              </div>
              <div>
                <label htmlFor="save-name">Nome</label>
                <input id="save-name" value={save.name} onChange={(e) => setSave({ ...save, name: e.target.value })} />
              </div>
            </div>
          )}
          <label htmlFor="save-notes">Nota da versão</label>
          <input id="save-notes" value={save.notes} onChange={(e) => setSave({ ...save, notes: e.target.value })} />
          <div className="row">
            <button className="secondary" onClick={() => setSaveOpen(false)}>
              Cancelar
            </button>
            <button className="primary" disabled={!save.target && !save.slug.trim()} onClick={saveAsTemplate}>
              Guardar
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
