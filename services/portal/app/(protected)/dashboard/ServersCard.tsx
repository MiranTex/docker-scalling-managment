"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import UsageBar from "@/components/UsageBar";
import { api, type NodeView } from "@/lib/platformClient";
import { bytes, diskPercent, memoryPercent, memoryUsed, warnings } from "@/lib/hostMetrics";

// ServersCard: as máquinas de relance no início -- CPU, memória e disco de
// cada nó, com aviso quando um disco enche ou a memória acaba.
export default function ServersCard() {
  const [nodes, setNodes] = useState<NodeView[] | null>(null);

  useEffect(() => {
    const load = () => api<NodeView[]>("nodes").then(setNodes).catch(() => setNodes(null));
    load();
    const t = setInterval(load, 30000);
    return () => clearInterval(t);
  }, []);

  if (!nodes || nodes.length === 0) return null;
  const alerts = nodes.flatMap((n) => (n.usage ? warnings(n.usage).map((w) => `${n.name}: ${w}`) : []));

  return (
    <div className="card full">
      <div className="page-header">
        <h2>Servidores</h2>
        <Link href="/admin/nodes">ver nós</Link>
      </div>
      {alerts.length > 0 && <div className="error">Atenção -- {alerts.join(" · ")}</div>}
      <table>
        <thead>
          <tr>
            <th>Nó</th>
            <th>CPU</th>
            <th>Memória</th>
            <th>Disco</th>
          </tr>
        </thead>
        <tbody>
          {nodes.map((n) => (
            <tr key={n.name}>
              <td>
                <Link href={`/admin/nodes/${encodeURIComponent(n.name)}`}>{n.name}</Link>
                {!n.online && <div className="muted">{n.status}</div>}
              </td>
              {n.usage ? (
                <>
                  <td>
                    <UsageBar percent={n.usage.cpuPercent} detail={`load ${n.usage.load1.toFixed(2)}`} />
                  </td>
                  <td>
                    <UsageBar percent={memoryPercent(n.usage)} detail={`${bytes(memoryUsed(n.usage))} de ${bytes(n.usage.memoryTotalBytes)}`} />
                  </td>
                  <td>
                    <UsageBar percent={diskPercent(n.usage)} detail={`${bytes(n.usage.diskAvailableBytes)} livres`} />
                  </td>
                </>
              ) : (
                <td colSpan={3} className="muted">
                  {n.online ? "a medir…" : "sem sinal"}
                </td>
              )}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
