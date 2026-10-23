"use client";

import PageHeader from "@/components/PageHeader";
import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import type { InstanceSummary } from "@/lib/autoscalerAdminClient";

type ListResponse = { instances: InstanceSummary[] };

export default function AutoscalerListClient() {
  const router = useRouter();
  const [instances, setInstances] = useState<InstanceSummary[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);

  async function loadInstances() {
    try {
      const res = await fetch("/api/admin/autoscaler");
      if (!res.ok) {
        setListError(`erro ${res.status} a listar autoscalers`);
        return;
      }
      const data: ListResponse = await res.json();
      setInstances(data.instances);
      setListError(null);
    } catch {
      setListError("erro a listar autoscalers");
    }
  }

  useEffect(() => {
    loadInstances();
    // Sondagem periódica -- réplicas/CPU mudam continuamente por conta do
    // próprio reconcile loop de cada group, não só por ação do utilizador.
    const interval = setInterval(loadInstances, 5000);
    return () => clearInterval(interval);
  }, []);

  return (
    <>
    <PageHeader
      title="Autoscalers"
      description="Cada linha é um autoscaler-group vivo, descoberto dinamicamente contra o launcher (GET /v1/groups)."
      actions={
        <Link href="/admin/autoscaler/new" className="primary">
          Criar grupo
        </Link>
      }
    />
    <div className="card wide">
      {listError && <div className="error">{listError}</div>}
      {!instances ? (
        <p className="muted">A carregar...</p>
      ) : instances.length === 0 ? (
        <p className="muted">Nenhum autoscaler-group vivo encontrado.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Instância</th>
              <th>Serviço</th>
              <th>Rede</th>
              <th>Réplicas</th>
              <th>Estado</th>
              <th>Público</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {instances.map((i) => (
              <tr key={i.id} onClick={() => router.push(`/admin/autoscaler/${i.id}`)} style={{ cursor: "pointer" }}>
                <td>{i.label}</td>
                <td>{i.status?.target_service ?? "-"}</td>
                <td>
                  <code>{i.network || "-"}</code>
                </td>
                <td>
                  {i.status ? `${i.status.replica_count} (min ${i.status.policy.min_replicas} / max ${i.status.policy.max_replicas})` : "-"}
                </td>
                <td>
                  <span className={`badge ${i.error ? "revoked" : "active"}`}>{i.error ?? "ok"}</span>
                </td>
                <td onClick={(e) => e.stopPropagation()}>
                  {i.exposedHost ? (
                    <a href={`${i.exposedScheme || "http"}://${i.exposedHost}`} target="_blank" rel="noreferrer">
                      {i.exposedHost}
                    </a>
                  ) : (
                    "--"
                  )}
                </td>
                <td>
                  <Link href={`/admin/autoscaler/${i.id}`} className="muted" onClick={(e) => e.stopPropagation()}>
                    Detalhes →
                  </Link>
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
