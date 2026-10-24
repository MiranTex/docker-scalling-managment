"use client";

import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { api, projectPath, type Project } from "@/lib/platformClient";

type Entry = { project: Project; orgId: number };

const dashboard = (orgId: number) => `/grafana/d/base-stack-projects/projetos?orgId=${orgId}`;

// Lista os projetos da conta, cada um com o link para o dashboard na sua
// organização do Grafana. Com um só projeto (e sem ser admin) vai direto.
// Os links são <a> e não <Link>: /grafana é um route handler.
export default function MetricsClient({ platformAdmin }: { platformAdmin: boolean }) {
  const [entries, setEntries] = useState<Entry[] | null>(null);
  const [platformOrg, setPlatformOrg] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const projects = await api<Project[]>("projects");
        const found = await Promise.all(
          projects.map((p) =>
            api<{ orgId: number }>(projectPath(p.slug, "grafana"))
              .then((r) => ({ project: p, orgId: r.orgId }))
              .catch(() => null),
          ),
        );
        const list = found.filter((e): e is Entry => e !== null && e.orgId > 0);
        if (!platformAdmin && list.length === 1) {
          window.location.replace(dashboard(list[0].orgId));
          return;
        }
        setEntries(list);
      } catch (e) {
        setError((e as Error).message);
      }
    })();
  }, [platformAdmin]);

  // A organização "Plataforma" (todos os projetos) só existe para admins;
  // o id vem do próprio Grafana.
  useEffect(() => {
    if (!platformAdmin) return;
    fetch("/grafana/api/user/orgs")
      .then((r) => (r.ok ? r.json() : []))
      .then((orgs: { orgId: number; name: string }[]) => setPlatformOrg(orgs.find((o) => o.name === "Plataforma")?.orgId ?? null))
      .catch(() => setPlatformOrg(null));
  }, [platformAdmin]);

  return (
    <>
      <PageHeader
        title="Métricas e logs"
        description="Cada projeto abre no Grafana só com os seus dados. Para mudar de projeto, volta a esta página."
      />

      <div className="card wide">
        {error && <div className="error">{error}</div>}
        {platformOrg && (
          <p>
            <a href={dashboard(platformOrg)} target="_blank" rel="noreferrer">
              Plataforma -- todos os projetos ↗
            </a>
          </p>
        )}
        {!entries ? (
          !error && <p className="muted">A carregar...</p>
        ) : entries.length === 0 ? (
          <p className="muted">Ainda não és membro de nenhum projeto -- pede a um administrador que te adicione.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Projeto</th>
                <th>Slug</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(({ project, orgId }) => (
                <tr key={project.id}>
                  <td>
                    <a href={dashboard(orgId)} target="_blank" rel="noreferrer">
                      {project.name} ↗
                    </a>
                  </td>
                  <td>
                    <code>{project.slug}</code>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
