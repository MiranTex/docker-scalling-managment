"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import Breadcrumbs from "@/components/Breadcrumbs";
import PageHeader from "@/components/PageHeader";
import TabBar, { useActiveTab } from "@/components/TabBar";
import { api, envPath } from "@/lib/platformClient";
import ConfirmModal from "../../../admin/database/ConfirmModal";
import AddonsTab from "./AddonsTab";
import JobsTab from "./JobsTab";
import SecretsTab from "./SecretsTab";
import ServicesTab from "./ServicesTab";

const TABS = [
  { key: "services", label: "Serviços" },
  { key: "addons", label: "Addons" },
  { key: "jobs", label: "Jobs" },
  { key: "secrets", label: "Segredos" },
  { key: "settings", label: "Definições" },
] as const;

export default function EnvironmentClient({ project, env }: { project: string; env: string }) {
  const router = useRouter();
  const tab = useActiveTab(TABS);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [purge, setPurge] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function deleteEnv() {
    setConfirmDelete(false);
    try {
      await api(`${envPath(project, env)}${purge ? "?purge=true" : ""}`, { method: "DELETE" });
      router.push(`/projects/${project}`);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <>
      <Breadcrumbs
        items={[{ label: "Projetos", href: "/projects" }, { label: project, href: `/projects/${project}` }, { label: env }]}
      />
      <PageHeader title={`${project} · ${env}`} />
      {error && <div className="error">{error}</div>}
      <TabBar tabs={TABS} active={tab} />

      <div className="card full">
        {tab === "services" && <ServicesTab project={project} env={env} />}
        {tab === "addons" && <AddonsTab project={project} env={env} />}
        {tab === "jobs" && <JobsTab project={project} env={env} />}
        {tab === "secrets" && <SecretsTab project={project} env={env} />}
        {tab === "settings" && (
          <>
            <h2>Apagar ambiente</h2>
            <p className="muted">Para e remove todos os containers deste ambiente e a sua rede.</p>
            <label className="checkbox">
              <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} /> Apagar também os
              dados
            </label>
            <button className="danger" onClick={() => setConfirmDelete(true)}>
              Apagar ambiente
            </button>
          </>
        )}
      </div>

      {confirmDelete && (
        <ConfirmModal
          title="Apagar ambiente"
          message={`Isto remove tudo o que corre em ${project}/${env}${purge ? " E OS DADOS" : ""}.`}
          expectedText={env}
          confirmLabel="Apagar"
          onConfirm={deleteEnv}
          onCancel={() => setConfirmDelete(false)}
        />
      )}
    </>
  );
}
