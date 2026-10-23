"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { api, envPath } from "@/lib/platformClient";
import ConfirmModal from "../../../admin/database/ConfirmModal";
import AddonsTab from "./AddonsTab";
import JobsTab from "./JobsTab";
import SecretsTab from "./SecretsTab";
import ServicesTab from "./ServicesTab";

// Cada separador é um componente próprio que recebe projeto+ambiente.
const TABS = [
  { key: "services", label: "Serviços", Component: ServicesTab },
  { key: "addons", label: "Addons", Component: AddonsTab },
  { key: "jobs", label: "Jobs", Component: JobsTab },
  { key: "secrets", label: "Segredos", Component: SecretsTab },
] as const;

type TabKey = (typeof TABS)[number]["key"];

export default function EnvironmentClient({ project, env }: { project: string; env: string }) {
  const router = useRouter();
  const [tab, setTab] = useState<TabKey>(TABS[0].key);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [purge, setPurge] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const Active = TABS.find((t) => t.key === tab)!.Component;

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
      <div className="card full">
        <p className="muted">
          <Link href="/projects">Projetos</Link> / <Link href={`/projects/${project}`}>{project}</Link> / {env}
        </p>
        <h1>
          {project} · {env}
        </h1>
        {error && <div className="error">{error}</div>}
        <div className="tabs">
          {TABS.map((t) => (
            <button key={t.key} className={t.key === tab ? "tab active" : "tab"} onClick={() => setTab(t.key)}>
              {t.label}
            </button>
          ))}
        </div>
        <Active project={project} env={env} />
      </div>

      <div className="card full">
        <h2>Apagar ambiente</h2>
        <p className="muted">Para e remove todos os containers deste ambiente e a sua rede.</p>
        <label className="checkbox">
          <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} /> Apagar também os dados
        </label>
        <button className="danger" onClick={() => setConfirmDelete(true)}>
          Apagar ambiente
        </button>
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
