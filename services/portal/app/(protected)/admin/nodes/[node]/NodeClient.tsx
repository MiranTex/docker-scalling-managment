"use client";

import Loading from "@/components/Skeleton";
import Breadcrumbs from "@/components/Breadcrumbs";
import LineChart from "@/components/LineChart";
import UsageBar from "@/components/UsageBar";
import { useEffect, useState } from "react";
import { api, type ContainerUsage, type HostSample, type NodeView } from "@/lib/platformClient";
import { bytes, diskPercent, diskUsed, memoryPercent, memoryUsed, warnings } from "@/lib/hostMetrics";

const RANGES = [1, 6, 24];

// NodeClient: uma máquina -- o que está a usar agora, as últimas horas e
// que containers gastam mais.
export default function NodeClient({ node }: { node: string }) {
  const [view, setView] = useState<NodeView | null>(null);
  const [samples, setSamples] = useState<HostSample[] | null>(null);
  const [hours, setHours] = useState(6);
  const [top, setTop] = useState<ContainerUsage[] | null>(null);
  const [loadingTop, setLoadingTop] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const path = (...rest: string[]) => ["nodes", node, ...rest].map(encodeURIComponent).join("/");

  async function load() {
    try {
      const [nodes, series] = await Promise.all([api<NodeView[]>("nodes"), api<HostSample[]>(`${path("metrics")}?hours=${hours}`)]);
      const v = nodes.find((n) => n.name === node);
      if (!v) {
        setError(`O nó ${node} não existe.`);
        return;
      }
      setView(v);
      setSamples(series);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function loadTop() {
    setLoadingTop(true);
    try {
      setTop(await api<ContainerUsage[]>(path("top")));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoadingTop(false);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(load, 30000);
    return () => clearInterval(t);
  }, [node, hours]);

  if (!view) return error ? <div className="error">{error}</div> : <Loading />;
  const u = view.usage;
  const times = samples?.map((s) => s.at) ?? [];
  const pct = (v: number) => `${Math.round(v)}%`;

  return (
    <>
      <div className="card full">
        <Breadcrumbs items={[{ label: "Nós", href: "/admin/nodes" }, { label: node }]} />
        <h1>
          {node}{" "}
          {view.online ? <span className="badge active">online</span> : <span className="badge pending">{view.status}</span>}
        </h1>
        {error && <div className="error">{error}</div>}
        {u && warnings(u).length > 0 && <div className="error">Atenção: {warnings(u).join(" · ")}</div>}
        <p className="muted">
          <code>
            {view.os}/{view.arch}
          </code>{" "}
          · {view.cpus} vCPU · Docker {view.dockerVersion}
          {view.kind === "agent" && ` · agente ${view.agentVersion}`}
          {view.address && (
            <>
              {" "}
              · <code>{view.address}</code>
            </>
          )}
        </p>
        {(Object.keys(view.autoLabels ?? {}).length > 0 || Object.keys(view.labels ?? {}).length > 0) && (
          <div className="row" style={{ gap: 4 }}>
            {Object.entries({ ...view.autoLabels, ...view.labels }).map(([k, v]) => (
              <span key={k} className="badge label">
                {k}={v}
              </span>
            ))}
          </div>
        )}
      </div>

      <div className="card full">
        <h2>Agora</h2>
        {!u ? (
          <p className="muted">{view.online ? "A medir -- a primeira leitura chega dentro de um minuto." : "O nó está sem sinal."}</p>
        ) : (
          <div className="form-grid">
            <div>
              <label>CPU</label>
              <UsageBar
                percent={u.cpuPercent}
                detail={`load ${u.load1.toFixed(2)} / ${u.load5.toFixed(2)} / ${u.load15.toFixed(2)}`}
                note={`reservado ${view.allocated.vcpu.toFixed(2)} de ${view.cpus} vCPU`}
              />
            </div>
            <div>
              <label>Memória</label>
              <UsageBar
                percent={memoryPercent(u)}
                detail={`${bytes(memoryUsed(u))} de ${bytes(u.memoryTotalBytes)}`}
                note={`reservado ${(view.allocated.memoryMb / 1024).toFixed(1)} de ${(view.capacityMemoryMb / 1024).toFixed(1)} GB`}
              />
            </div>
            <div>
              <label>Disco (dados do Docker)</label>
              <UsageBar percent={diskPercent(u)} detail={`${bytes(diskUsed(u))} de ${bytes(u.diskTotalBytes)} · ${bytes(u.diskAvailableBytes)} livres`} />
            </div>
          </div>
        )}
      </div>

      <div className="card full">
        <div className="page-header">
          <h2>Últimas horas</h2>
          <div className="row" style={{ gap: 6 }}>
            {RANGES.map((h) => (
              <button key={h} className={h === hours ? "primary" : "secondary"} onClick={() => setHours(h)}>
                {h} h
              </button>
            ))}
          </div>
        </div>
        <p className="muted">
          Em memória no launcher (24 h; um restart recomeça). Para mais tempo: o dashboard <strong>Servidores</strong> no
          Grafana.
        </p>
        {!samples ? (
          <Loading />
        ) : (
          <div className="form-grid">
            <div>
              <h3>CPU em uso</h3>
              <LineChart times={times} series={[{ label: "CPU", values: samples.map((s) => s.cpuPercent) }]} max={100} format={pct} />
            </div>
            <div>
              <h3>Carga</h3>
              <LineChart
                times={times}
                series={[
                  { label: "1 min", values: samples.map((s) => s.load1) },
                  { label: "15 min", values: samples.map((s) => s.load15) },
                ]}
                format={(v) => v.toFixed(1)}
              />
            </div>
            <div>
              <h3>Memória em uso</h3>
              <LineChart times={times} series={[{ label: "memória", values: samples.map(memoryPercent) }]} max={100} format={pct} />
            </div>
            <div>
              <h3>Disco em uso</h3>
              <LineChart times={times} series={[{ label: "disco", values: samples.map(diskPercent) }]} max={100} format={pct} />
            </div>
          </div>
        )}
      </div>

      <div className="card full">
        <div className="page-header">
          <h2>Containers que mais gastam</h2>
          <button className="secondary" disabled={!view.online || loadingTop} onClick={loadTop}>
            {loadingTop ? "A medir..." : top ? "Medir de novo" : "Medir agora"}
          </button>
        </div>
        {!top ? (
          <p className="muted">O Docker leva cerca de um segundo por container -- mede-se quando pedes.</p>
        ) : top.length === 0 ? (
          <p className="muted">Nenhum container a correr.</p>
        ) : (
          <div style={{ overflowX: "auto" }}>
            <table>
              <thead>
                <tr>
                  <th>Container</th>
                  <th>Projeto / ambiente</th>
                  <th>CPU</th>
                  <th>Memória</th>
                </tr>
              </thead>
              <tbody>
                {top.slice(0, 15).map((c) => (
                  <tr key={c.name}>
                    <td>
                      <code>{c.name}</code>
                      {c.service && <div className="muted">{c.service}</div>}
                    </td>
                    <td>{c.project ? `${c.project} / ${c.environment}` : <span className="muted">plataforma</span>}</td>
                    <td>{c.cpuPercent.toFixed(1)}%</td>
                    <td>
                      {bytes(c.memoryBytes)}
                      {c.memoryLimitBytes > 0 && c.memoryLimitBytes < (u?.memoryTotalBytes ?? 2 ** 50) && (
                        <span className="muted"> de {bytes(c.memoryLimitBytes)}</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </>
  );
}
