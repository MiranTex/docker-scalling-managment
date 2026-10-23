"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { api, type Project } from "@/lib/platformClient";

export default function ProjectsClient() {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  async function load() {
    try {
      setProjects(await api<Project[]>("projects"));
      setListError(null);
    } catch (e) {
      setListError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, []);

  async function handleCreate() {
    setFormError(null);
    setSaving(true);
    try {
      await api("projects", { method: "POST", body: JSON.stringify({ slug: slug.trim(), name: name.trim() }) });
      setSlug("");
      setName("");
      await load();
    } catch (e) {
      setFormError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Projetos</h1>
        <p className="muted">
          Cada projeto tem ambientes isolados (rede, segredos, serviços e bases de dados próprios). Nada é
          partilhado entre projetos.
        </p>
        {listError && <div className="error">{listError}</div>}
        {!projects ? (
          <p className="muted">A carregar...</p>
        ) : projects.length === 0 ? (
          <p className="muted">Nenhum projeto ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Projeto</th>
                <th>Slug</th>
                <th>Criado</th>
              </tr>
            </thead>
            <tbody>
              {projects.map((p) => (
                <tr key={p.id}>
                  <td>
                    <Link href={`/projects/${p.slug}`}>{p.name}</Link>
                  </td>
                  <td>
                    <code>{p.slug}</code>
                  </td>
                  <td>{new Date(p.createdAt).toLocaleString("pt-PT")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card wide">
        <h1>Novo projeto</h1>
        {formError && <div className="error">{formError}</div>}
        <label htmlFor="project-slug">Slug</label>
        <input
          id="project-slug"
          value={slug}
          onChange={(e) => setSlug(e.target.value)}
          placeholder="loja"
        />
        <p className="muted">Fixo depois de criado: entra nos nomes de redes, containers e hostnames.</p>
        <label htmlFor="project-name">Nome</label>
        <input id="project-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Loja online" />
        <button className="secondary" disabled={saving || !slug.trim()} onClick={handleCreate}>
          {saving ? "A criar..." : "Criar projeto"}
        </button>
      </div>
    </>
  );
}
