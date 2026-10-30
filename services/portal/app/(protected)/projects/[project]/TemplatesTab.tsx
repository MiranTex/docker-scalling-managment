"use client";

import Loading from "@/components/Skeleton";
import Link from "next/link";
import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { api, projectPath, type Template } from "@/lib/platformClient";
import SpecForm, { emptySpecForm, specFromForm } from "./templates/SpecForm";
import { useCan } from "@/lib/projectRole";

export default function TemplatesTab({ project }: { project: string }) {
  const canEdit = useCan("developer");
  const [list, setList] = useState<Template[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [form, setForm] = useState(emptySpecForm);
  const [saving, setSaving] = useState(false);
  const [showArchived, setShowArchived] = useState(false);

  async function load() {
    try {
      setList(await api<Template[]>(projectPath(project, "templates")));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project]);

  async function create() {
    setError(null);
    setSaving(true);
    try {
      await api(projectPath(project, "templates"), {
        method: "POST",
        body: JSON.stringify({ slug: slug.trim(), name: name.trim(), image: form.image.trim(), notes: form.notes.trim(), spec: specFromForm(form) }),
      });
      setOpen(false);
      setSlug("");
      setName("");
      setForm(emptySpecForm());
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  const shown = (list ?? []).filter((t) => showArchived || !t.archivedAt);

  return (
    <div className="card wide">
      <div className="page-header">
        <p className="muted" style={{ margin: 0 }}>
          Configurações reutilizáveis para criar serviços neste projeto. Cada gravação cria uma versão nova; os serviços
          já criados não mudam sozinhos.
        </p>
        {canEdit && (
        <button className="primary" onClick={() => setOpen(true)}>
          Novo modelo
        </button>
        )}
      </div>
      {!open && error && <div className="error">{error}</div>}
      {!list ? (
        <Loading />
      ) : shown.length === 0 ? (
        <p className="muted">Ainda sem modelos.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Modelo</th>
              <th>Versão</th>
              <th>Usado por</th>
              <th>Criado</th>
            </tr>
          </thead>
          <tbody>
            {shown.map((t) => (
              <tr key={t.id}>
                <td>
                  <Link href={`/projects/${project}/templates/${t.slug}`}>
                    <strong>{t.name}</strong>
                  </Link>{" "}
                  <code>{t.slug}</code>
                  {t.archivedAt && <span className="badge"> arquivado</span>}
                  {t.description && <div className="muted">{t.description}</div>}
                </td>
                <td>v{t.latestVersion}</td>
                <td>{t.usedBy} serviço(s)</td>
                <td>{new Date(t.createdAt).toLocaleString("pt-PT")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {(list ?? []).some((t) => t.archivedAt) && (
        <label className="checkbox">
          <input type="checkbox" checked={showArchived} onChange={(e) => setShowArchived(e.target.checked)} /> mostrar
          arquivados
        </label>
      )}

      {open && (
        <Modal title="Novo modelo" onClose={() => setOpen(false)} width={760}>
          {error && <div className="error">{error}</div>}
          <div className="form-grid">
            <div>
              <label htmlFor="tpl-slug">Slug</label>
              <input id="tpl-slug" value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="laravel-web" />
            </div>
            <div>
              <label htmlFor="tpl-name">Nome</label>
              <input id="tpl-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Laravel (web)" />
            </div>
          </div>
          <SpecForm form={form} onChange={setForm} />
          <div className="row">
            <button className="secondary" onClick={() => setOpen(false)}>
              Cancelar
            </button>
            <button className="primary" disabled={saving || !slug.trim() || !form.image.trim()} onClick={create}>
              {saving ? "A criar..." : "Criar modelo (v1)"}
            </button>
          </div>
        </Modal>
      )}
    </div>
  );
}
