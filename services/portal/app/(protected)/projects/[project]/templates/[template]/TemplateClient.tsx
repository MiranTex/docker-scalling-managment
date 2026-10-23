"use client";

import { useEffect, useState } from "react";
import Breadcrumbs from "@/components/Breadcrumbs";
import Modal from "@/components/Modal";
import PageHeader from "@/components/PageHeader";
import { api, formatSpecValue, projectPath, SPEC_LABELS, type TemplateDetail, type TemplateVersion } from "@/lib/platformClient";
import SpecForm, { specFormFrom, specFromForm, type SpecFormState } from "../SpecForm";
import { useCan } from "@/lib/projectRole";

// Resumo de uma versão: só os campos com valor, para a lista não ficar
// enorme.
function SpecSummary({ v }: { v: TemplateVersion }) {
  const entries = Object.entries(v.spec).filter(([, value]) => formatSpecValue(value) !== "—");
  return (
    <table>
      <tbody>
        <tr>
          <td className="muted">Imagem (primeiro deploy)</td>
          <td>
            <code>{v.image}</code>
          </td>
        </tr>
        {entries.map(([k, value]) => (
          <tr key={k}>
            <td className="muted">{SPEC_LABELS[k] ?? k}</td>
            <td style={{ whiteSpace: "pre-wrap" }}>
              <code>{formatSpecValue(value)}</code>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export default function TemplateClient({ project, template }: { project: string; template: string }) {
  const canEdit = useCan("developer");
  const [detail, setDetail] = useState<TemplateDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selected, setSelected] = useState<number | null>(null);
  const [form, setForm] = useState<SpecFormState | null>(null);
  const [saving, setSaving] = useState(false);
  const [confirmArchive, setConfirmArchive] = useState(false);
  const path = (...rest: string[]) => projectPath(project, "templates", template, ...rest);

  async function load() {
    try {
      const d = await api<TemplateDetail>(path());
      setDetail(d);
      setSelected((s) => s ?? d.latestVersion);
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project, template]);

  function newVersion() {
    const latest = detail?.versions[0];
    if (latest) setForm(specFormFrom(latest.spec, latest.image));
  }

  async function saveVersion() {
    if (!form) return;
    setError(null);
    setSaving(true);
    try {
      const v = await api<TemplateVersion>(path("versions"), {
        method: "POST",
        body: JSON.stringify({ image: form.image.trim(), notes: form.notes.trim(), spec: specFromForm(form) }),
      });
      setForm(null);
      setSelected(v.version);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function archive() {
    setConfirmArchive(false);
    try {
      await api(path(), { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const current = detail?.versions.find((v) => v.version === selected);

  return (
    <>
      <Breadcrumbs
        items={[
          { label: "Projetos", href: "/projects" },
          { label: project, href: `/projects/${project}?tab=templates` },
          { label: template },
        ]}
      />
      <PageHeader
        title={detail ? `${detail.name} · v${detail.latestVersion}` : template}
        description={detail?.description || "Cada versão é imutável. Os serviços criados a partir deste modelo mostram quando há uma versão nova."}
        actions={
          detail &&
          !detail.archivedAt &&
          canEdit && (
            <>
              <button className="primary" onClick={newVersion}>
                Nova versão
              </button>
              <button className="danger" onClick={() => setConfirmArchive(true)}>
                Arquivar
              </button>
            </>
          )
        }
      />
      {error && !form && <div className="error">{error}</div>}
      {detail?.archivedAt && <div className="notice">Arquivado: já não se criam serviços a partir dele. Os existentes não mudam.</div>}

      {detail && (
        <div className="card wide">
          <p className="muted">Usado por {detail.usedBy} serviço(s).</p>
          <h2>Versões</h2>
          <table>
            <thead>
              <tr>
                <th>Versão</th>
                <th>Nota</th>
                <th>Criada</th>
              </tr>
            </thead>
            <tbody>
              {detail.versions.map((v) => (
                <tr key={v.id} onClick={() => setSelected(v.version)} style={{ cursor: "pointer" }}>
                  <td>
                    <strong>v{v.version}</strong> {v.version === selected && <span className="badge active">a ver</span>}
                  </td>
                  <td>{v.notes || <span className="muted">—</span>}</td>
                  <td>
                    {new Date(v.createdAt).toLocaleString("pt-PT")} · {v.createdBy}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {current && (
            <>
              <h2>Configuração da v{current.version}</h2>
              <SpecSummary v={current} />
            </>
          )}
        </div>
      )}

      {form && (
        <Modal title={`Nova versão (v${(detail?.latestVersion ?? 0) + 1})`} onClose={() => setForm(null)} width={760}>
          {error && <div className="error">{error}</div>}
          <p className="muted">Parte da versão mais recente. O tipo (web/worker) não muda entre versões.</p>
          <SpecForm form={form} onChange={setForm} kindLocked />
          <div className="row">
            <button className="secondary" onClick={() => setForm(null)}>
              Cancelar
            </button>
            <button className="primary" disabled={saving || !form.image.trim()} onClick={saveVersion}>
              {saving ? "A gravar..." : "Gravar versão"}
            </button>
          </div>
        </Modal>
      )}
      {confirmArchive && (
        <Modal title="Arquivar modelo" onClose={() => setConfirmArchive(false)}>
          <p>Deixa de se poder criar serviços a partir deste modelo. Os serviços existentes não mudam.</p>
          <div className="row">
            <button className="secondary" onClick={() => setConfirmArchive(false)}>
              Cancelar
            </button>
            <button className="danger" onClick={archive}>
              Arquivar
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
