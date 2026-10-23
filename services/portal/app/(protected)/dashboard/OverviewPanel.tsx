"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import type { DbStatus } from "@/lib/dbAdminClient";
import type { Capacity, LauncherInstance, ReplicaSummary } from "@/lib/launcherClient";
import type { InstanceSummary } from "@/lib/autoscalerAdminClient";
import { api, type Project } from "@/lib/platformClient";
import { formatMemory } from "@/lib/instanceTypes";
import CapacityPanel from "../admin/launcher/CapacityPanel";
import { formatBytes } from "../admin/database/format";

async function getJson<T>(url: string): Promise<T | null> {
  try {
    const res = await fetch(url);
    return res.ok ? ((await res.json()) as T) : null;
  } catch {
    return null;
  }
}

function Stat({ label, value, href, hint }: { label: string; value: React.ReactNode; href: string; hint?: string }) {
  return (
    <Link href={href} className="stat">
      <span className="stat-label">{label}</span>
      <span className="stat-value">{value}</span>
      {hint && <span className="muted">{hint}</span>}
    </Link>
  );
}

export default function OverviewPanel() {
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [instances, setInstances] = useState<LauncherInstance[] | null>(null);
  const [replicas, setReplicas] = useState<ReplicaSummary[] | null>(null);
  const [capacity, setCapacity] = useState<Capacity | null>(null);
  const [db, setDb] = useState<DbStatus | null>(null);
  const [groups, setGroups] = useState<InstanceSummary[] | null>(null);

  useEffect(() => {
    api<Project[]>("projects").then(setProjects).catch(() => setProjects(null));
    getJson<LauncherInstance[]>("/api/admin/launcher").then(setInstances);
    getJson<ReplicaSummary[]>("/api/admin/launcher/replicas").then(setReplicas);
    getJson<Capacity>("/api/admin/launcher/capacity").then(setCapacity);
    getJson<DbStatus>("/api/admin/database").then(setDb);
    getJson<{ instances: InstanceSummary[] }>("/api/admin/autoscaler").then((d) => setGroups(d?.instances ?? null));
  }, []);

  const running =
    instances && replicas
      ? instances.filter((i) => i.status === "running").length + replicas.filter((r) => r.state === "running").length
      : null;
  const dash = "--";

  return (
    <>
      <div className="stat-grid">
        <Stat label="Projetos" value={projects?.length ?? dash} href="/projects" />
        <Stat label="Containers a correr" value={running ?? dash} href="/admin/launcher" />
        <Stat label="Autoscalers" value={groups?.length ?? dash} href="/admin/autoscaler" />
        <Stat
          label="Base de dados"
          value={db ? formatBytes(db.postgres.database_size_bytes) : dash}
          href="/admin/database"
          hint={db ? `${db.postgres.connection_count} ligações` : undefined}
        />
      </div>

      <div className="split-rev">
        <div className="card wide">
          <h2>Capacidade do host</h2>
          {capacity ? (
            <>
              <CapacityPanel capacity={capacity} />
              <p className="muted">
                {capacity.containerCount} containers · {capacity.allocatedVcpu.toFixed(2)} vCPU ·{" "}
                {formatMemory(capacity.allocatedMemoryMb)} alocados
              </p>
            </>
          ) : (
            <p className="muted">A carregar...</p>
          )}
        </div>
        <div className="card wide">
          <h2>Backups</h2>
          {db ? (
            <p>
              Último backup:{" "}
              <strong>{db.last_backup_at ? new Date(db.last_backup_at).toLocaleString("pt-PT") : "nenhum ainda"}</strong>
              <br />
              <Link href="/admin/database/backups">Ver backups →</Link>
            </p>
          ) : (
            <p className="muted">A carregar...</p>
          )}
        </div>
      </div>
    </>
  );
}
