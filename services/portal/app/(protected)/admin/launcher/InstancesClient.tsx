"use client";

import Link from "next/link";
import { useEffect, useMemo, useState } from "react";
import type { Capacity, LauncherInstance, ReplicaSummary } from "@/lib/launcherClient";
import { formatMemory } from "@/lib/instanceTypes";
import ConfirmModal from "../database/ConfirmModal";
import CapacityPanel from "./CapacityPanel";

// Row unifica os dois tipos de container geridos por esta plataforma que
// aparecem em /admin/launcher: instâncias que este launcher lançou
// diretamente (kind "solo"/"group", vindas da sua store -- ver
// lib/launcherClient.listInstances) e réplicas de aplicação que um
// autoscaler-group criou por conta própria (kind "replica", descobertas
// contra o Docker -- ver lib/launcherClient.listReplicas). Sem esta
// segunda fonte, uma réplica lançada pelo autoscaler nunca aparecia aqui,
// só dentro do seu próprio group em /admin/autoscaler.
type Row = {
  key: string;
  kind: "solo" | "group" | "replica";
  label: string;
  active: boolean;
  statusText: string;
  error?: string;
  containerId: string;
  info: string;
  sizeText: string;
  exposedHost?: string;
  exposedScheme?: string;
};

type StatusFilter = "active" | "stopped" | "all";

function instanceToRow(i: LauncherInstance): Row {
  return {
    key: `instance:${i.id}`,
    kind: i.kind,
    label: i.templateName,
    active: i.status === "running",
    statusText: i.status,
    error: i.error,
    containerId: i.containerId,
    info: `${new Date(i.createdAt).toLocaleString("pt-PT")} · ${i.updatedBy}`,
    sizeText: i.instanceType ? `${i.instanceType} (${i.vcpu} vCPU / ${formatMemory(i.memoryMb)})` : "--",
    exposedHost: i.exposedHost,
    exposedScheme: i.exposedScheme,
  };
}

function replicaToRow(r: ReplicaSummary): Row {
  return {
    key: `replica:${r.containerId}`,
    kind: "replica",
    label: r.targetService,
    active: r.state === "running",
    statusText: r.state,
    containerId: r.containerId,
    info: `rede: ${r.network || "--"}`,
    // O tamanho de uma réplica vive nas labels do container, não na store
    // do launcher -- GET /v1/replicas ainda não as devolve.
    sizeText: "--",
    exposedHost: r.exposedHost,
    exposedScheme: r.exposedScheme,
  };
}

export default function InstancesClient() {
  const [instances, setInstances] = useState<LauncherInstance[] | null>(null);
  const [replicas, setReplicas] = useState<ReplicaSummary[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("active");
  const [capacity, setCapacity] = useState<Capacity | null>(null);
  const [actionTarget, setActionTarget] = useState<{ containerId: string; label: string; action: "stop" | "remove" } | null>(
    null
  );
  const [actionError, setActionError] = useState<string | null>(null);

  async function loadInstances() {
    try {
      const [instancesRes, replicasRes] = await Promise.all([
        fetch("/api/admin/launcher"),
        fetch("/api/admin/launcher/replicas"),
      ]);
      if (!instancesRes.ok) {
        setListError(`erro ${instancesRes.status} a listar instâncias`);
        return;
      }
      if (!replicasRes.ok) {
        setListError(`erro ${replicasRes.status} a listar réplicas`);
        return;
      }
      setInstances(await instancesRes.json());
      setReplicas(await replicasRes.json());
      setListError(null);
    } catch {
      setListError("erro a listar instâncias");
    }
  }

  async function loadCapacity() {
    try {
      const res = await fetch("/api/admin/launcher/capacity");
      if (res.ok) setCapacity(await res.json());
    } catch {
      // O painel de capacidade é informativo -- se falhar, o resto do ecrã
      // continua utilizável.
    }
  }

  useEffect(() => {
    loadInstances();
    loadCapacity();
  }, []);

  const rows = useMemo(() => {
    if (!instances || !replicas) return null;
    const all = [...instances.map(instanceToRow), ...replicas.map(replicaToRow)];
    if (statusFilter === "all") return all;
    return all.filter((row) => (statusFilter === "active" ? row.active : !row.active));
  }, [instances, replicas, statusFilter]);

  async function handleAction() {
    if (!actionTarget) return;
    const { containerId, action } = actionTarget;
    setActionTarget(null);
    setActionError(null);
    try {
      const res = await fetch(
        `/api/admin/launcher/containers/${encodeURIComponent(containerId)}${action === "stop" ? "/stop" : ""}`,
        { method: action === "stop" ? "POST" : "DELETE" }
      );
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setActionError(data.message ?? "erro ao aplicar ação no container");
        return;
      }
      await loadInstances();
      await loadCapacity();
    } catch {
      setActionError("erro ao aplicar ação no container");
    }
  }

  return (
    <>
      {capacity && (
        <div className="card wide">
          <CapacityPanel capacity={capacity} />
        </div>
      )}

      <div className="card wide">
        <div className="page-header">
          <h2 style={{ margin: 0 }}>Em execução</h2>
          <div className="row">
            <select
              aria-label="Filtrar por estado"
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}
            >
              <option value="active">Ativas</option>
              <option value="stopped">Paradas</option>
              <option value="all">Todas</option>
            </select>
            <Link href="/admin/launcher/new">
              <button className="primary">Lançar nova</button>
            </Link>
          </div>
        </div>
        <p className="muted">
          Parar/eliminar um <code>group</code> inteiro faz-se em <Link href="/admin/autoscaler">/admin/autoscaler</Link>;
          as suas réplicas podem ser paradas ou eliminadas individualmente aqui.
        </p>
        {listError && <div className="error">{listError}</div>}
        {actionError && <div className="error">{actionError}</div>}

        {!rows ? (
          <p className="muted">A carregar...</p>
        ) : rows.length === 0 ? (
          <p className="muted">Nenhuma instância neste filtro.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nome</th>
                <th>Tipo</th>
                <th>Tamanho</th>
                <th>Estado</th>
                <th>Container</th>
                <th>Info</th>
                <th>Público</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.key}>
                  <td>
                    <code>{row.label}</code>
                  </td>
                  <td>{row.kind}</td>
                  <td>{row.sizeText}</td>
                  <td>
                    {row.statusText}
                    {row.error && <div className="error">{row.error}</div>}
                  </td>
                  <td>
                    <code>{row.containerId ? row.containerId.slice(0, 12) : "--"}</code>
                  </td>
                  <td>{row.info}</td>
                  <td>
                    {row.exposedHost ? (
                      <a href={`${row.exposedScheme || "http"}://${row.exposedHost}`} target="_blank" rel="noreferrer">
                        {row.exposedHost}
                      </a>
                    ) : (
                      "--"
                    )}
                  </td>
                  <td>
                    {row.kind === "group" ? (
                      row.active && (
                        <Link href="/admin/autoscaler" className="muted">
                          gerir em /admin/autoscaler
                        </Link>
                      )
                    ) : (
                      row.containerId && (
                        <div className="row">
                          {row.active && (
                            <button
                              className="secondary"
                              onClick={() => setActionTarget({ containerId: row.containerId, label: row.label, action: "stop" })}
                            >
                              Parar
                            </button>
                          )}
                          <button
                            className="danger"
                            onClick={() => setActionTarget({ containerId: row.containerId, label: row.label, action: "remove" })}
                          >
                            Eliminar
                          </button>
                        </div>
                      )
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {actionTarget && (
        <ConfirmModal
          title={actionTarget.action === "stop" ? "Confirmar paragem" : "Confirmar eliminação"}
          message={
            actionTarget.action === "stop"
              ? `Isto para o container de "${actionTarget.label}" (${actionTarget.containerId.slice(0, 12)}) -- o container fica parado, não é removido.`
              : `Isto para e remove definitivamente o container de "${actionTarget.label}" (${actionTarget.containerId.slice(0, 12)}).`
          }
          expectedText={actionTarget.containerId.slice(0, 12)}
          confirmLabel={actionTarget.action === "stop" ? "Parar" : "Eliminar"}
          onConfirm={handleAction}
          onCancel={() => setActionTarget(null)}
        />
      )}
    </>
  );
}
