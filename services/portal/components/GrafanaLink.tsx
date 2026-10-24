"use client";

import { useEffect, useState } from "react";
import { api, projectPath } from "@/lib/platformClient";

// Botão que abre o dashboard "Projetos" deste projeto no Grafana, na
// organização do projeto (só com os dados dele). Um <a> e não um <Link>:
// /grafana é servido por um route handler, não por uma página.
export default function GrafanaLink({ project }: { project: string }) {
  const [orgId, setOrgId] = useState<number | null>(null);
  useEffect(() => {
    api<{ orgId: number }>(projectPath(project, "grafana"))
      .then((r) => setOrgId(r.orgId))
      .catch(() => setOrgId(null));
  }, [project]);
  if (!orgId) return null;
  return (
    <a className="secondary" href={`/grafana/d/base-stack-projects/projetos?orgId=${orgId}`} target="_blank" rel="noreferrer">
      Métricas e logs
    </a>
  );
}
