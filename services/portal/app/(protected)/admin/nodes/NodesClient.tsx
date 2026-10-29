"use client";

import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { api, type NodeView } from "@/lib/platformClient";

const gb = (mb: number) => `${(mb / 1024).toFixed(1)} GB`;

// As máquinas onde correm as apps e os addons dos projetos. Para já só a
// "local" (a do launcher); as outras entram com "basestack node join".
export default function NodesClient() {
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  async function load() {
    try {
      setNodes(await api<NodeView[]>("nodes"));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, []);

  async function setSchedulable(n: NodeView, schedulable: boolean) {
    if (!schedulable && !confirm(`Deixar de pôr réplicas novas em ${n.name}? As que lá estão continuam a correr.`)) return;
    setBusy(n.name);
    try {
      await api(`nodes/${encodeURIComponent(n.name)}`, { method: "PATCH", body: JSON.stringify({ schedulable }) });
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <PageHeader title="Nós" description="As máquinas onde correm as apps e os addons dos projetos." />
      {error && <div className="error">{error}</div>}
      <div className="card wide">
        {!nodes ? (
          <p className="muted">A carregar...</p>
        ) : nodes.length === 0 ? (
          <p className="muted">Ainda nenhum nó registado -- o launcher regista a máquina local ao arrancar.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nó</th>
                <th>Estado</th>
                <th>Plataforma</th>
                <th>CPUs</th>
                <th>Memória alocada</th>
                <th>Réplicas</th>
                <th>Addons</th>
                <th>Último sinal</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {nodes.map((n) => {
                const pct = n.capacityMemoryMb > 0 ? Math.round((n.allocated.memoryMb / n.capacityMemoryMb) * 100) : 0;
                return (
                  <tr key={n.name}>
                    <td>
                      <strong>{n.name}</strong>
                      {n.address && (
                        <div className="muted">
                          <code>{n.address}</code>
                        </div>
                      )}
                    </td>
                    <td>
                      {!n.online ? (
                        <span className="badge failed">offline</span>
                      ) : n.schedulable ? (
                        <span className="badge active">ativo</span>
                      ) : (
                        <span className="badge pending">manutenção</span>
                      )}
                    </td>
                    <td>
                      <code>
                        {n.os}/{n.arch}
                      </code>
                      <div className="muted">Docker {n.dockerVersion}</div>
                    </td>
                    <td>
                      {n.allocated.vcpu.toFixed(2)} / {n.cpus}
                    </td>
                    <td>
                      {gb(n.allocated.memoryMb)} / {gb(n.capacityMemoryMb)} ({pct}%)
                    </td>
                    <td>{n.replicas}</td>
                    <td>{n.addons}</td>
                    <td>{new Date(n.lastSeenAt).toLocaleString("pt-PT")}</td>
                    <td>
                      {n.schedulable ? (
                        <button className="secondary" disabled={busy === n.name} onClick={() => setSchedulable(n, false)}>
                          Manutenção
                        </button>
                      ) : (
                        <button className="primary" disabled={busy === n.name} onClick={() => setSchedulable(n, true)}>
                          Reativar
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        <p className="muted" style={{ marginTop: 12 }}>
          A memória alocada é a soma dos limites dos containers (não o consumo real). Em manutenção, o nó não recebe
          réplicas novas; as que lá estão continuam a correr.
        </p>
      </div>
    </>
  );
}
