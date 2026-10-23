"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import Breadcrumbs from "@/components/Breadcrumbs";
import Modal from "@/components/Modal";
import PageHeader from "@/components/PageHeader";
import TabBar, { useActiveTab } from "@/components/TabBar";
import { api, projectPath, type ProjectDetail } from "@/lib/platformClient";
import ConfirmModal from "../../admin/database/ConfirmModal";
import DeployTokensCard from "./DeployTokensCard";
import RegistriesCard from "./RegistriesCard";
import SettingsCard from "./SettingsCard";

const TABS = [
  { key: "environments", label: "Ambientes" },
  { key: "tokens", label: "Deploy tokens" },
  { key: "registries", label: "Registries" },
  { key: "settings", label: "Definições" },
] as const;

export default function ProjectClient({ project }: { project: string }) {
  const router = useRouter();
  const tab = useActiveTab(TABS);
  const [detail, setDetail] = useState<ProjectDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [envSlug, setEnvSlug] = useState("");
  const [envError, setEnvError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [creatingEnv, setCreatingEnv] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [purge, setPurge] = useState(false);

  async function load() {
    try {
      setDetail(await api<ProjectDetail>(projectPath(project)));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project]);

  async function createEnv() {
    setEnvError(null);
    setCreating(true);
    try {
      await api(projectPath(project, "environments"), { method: "POST", body: JSON.stringify({ slug: envSlug.trim() }) });
      setEnvSlug("");
      setCreatingEnv(false);
      await load();
    } catch (e) {
      setEnvError((e as Error).message);
    } finally {
      setCreating(false);
    }
  }

  async function deleteProject() {
    setConfirmDelete(false);
    try {
      await api(`${projectPath(project)}${purge ? "?purge=true" : ""}`, { method: "DELETE" });
      router.push("/projects");
    } catch (e) {
      setError((e as Error).message);
    }
  }

  if (!detail) {
    return (
      <div className="card wide">
        <h1>{project}</h1>
        {error ? <div className="error">{error}</div> : <p className="muted">A carregar...</p>}
      </div>
    );
  }

  return (
    <>
      <Breadcrumbs items={[{ label: "Projetos", href: "/projects" }, { label: detail.slug }]} />
      <PageHeader title={detail.name} description={`${detail.environments.length} ambiente(s)`} />
      {error && <div className="error">{error}</div>}
      <TabBar tabs={TABS} active={tab} />

      {tab === "environments" && (
        <div className="card wide">
          <div className="page-header">
            <h2 style={{ margin: 0 }}>Ambientes</h2>
            <button
              className="primary"
              onClick={() => {
                setEnvError(null);
                setCreatingEnv(true);
              }}
            >
              Novo ambiente
            </button>
          </div>
          {detail.environments.length === 0 ? (
            <p className="muted">Ainda sem ambientes. Cria pelo menos um (ex: production).</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Ambiente</th>
                  <th>Rede</th>
                  <th>Criado</th>
                </tr>
              </thead>
              <tbody>
                {detail.environments.map((e) => (
                  <tr key={e.id}>
                    <td>
                      <Link href={`/projects/${detail.slug}/${e.slug}`}>{e.slug}</Link>
                    </td>
                    <td>
                      <code>{e.network}</code>
                    </td>
                    <td>{new Date(e.createdAt).toLocaleString("pt-PT")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

      {creatingEnv && (
        <Modal title="Novo ambiente" onClose={() => setCreatingEnv(false)}>
          {envError && <div className="error">{envError}</div>}
          <label htmlFor="env-slug">Slug</label>
          <input
            id="env-slug"
            value={envSlug}
            onChange={(e) => setEnvSlug(e.target.value)}
            placeholder="production"
            autoFocus
          />
          <div className="row">
            <button className="primary" disabled={creating || !envSlug.trim()} onClick={createEnv}>
              {creating ? "A criar..." : "Criar ambiente"}
            </button>
            <button className="secondary" onClick={() => setCreatingEnv(false)}>
              Cancelar
            </button>
          </div>
        </Modal>
      )}

      {tab === "tokens" && <DeployTokensCard project={project} environments={detail.environments} apiUrl={detail.apiUrl} />}

      {tab === "registries" && <RegistriesCard project={project} />}

      {tab === "settings" && (
        <>
          <SettingsCard detail={detail} onSaved={load} />
          <div className="card wide">
            <h2>Apagar projeto</h2>
            <p className="muted">
              Para e remove todos os containers e redes do projeto. Os volumes de dados (bases de dados dos addons) só
              são apagados se marcares a opção abaixo.
            </p>
            <label className="checkbox">
              <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} /> Apagar também os
              dados
            </label>
            <button className="danger" onClick={() => setConfirmDelete(true)}>
              Apagar projeto
            </button>
          </div>
        </>
      )}

      {confirmDelete && (
        <ConfirmModal
          title="Apagar projeto"
          message={`Isto remove todos os ambientes de "${detail.slug}"${purge ? " E OS DADOS" : ""}.`}
          expectedText={detail.slug}
          confirmLabel="Apagar"
          onConfirm={deleteProject}
          onCancel={() => setConfirmDelete(false)}
        />
      )}
    </>
  );
}
