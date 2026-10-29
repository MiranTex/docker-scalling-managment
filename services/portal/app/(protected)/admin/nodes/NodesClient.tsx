"use client";

import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import PageHeader from "@/components/PageHeader";
import { api, type NodeCheck, type NodeJoinToken, type NodeView } from "@/lib/platformClient";

const gb = (mb: number) => `${(mb / 1024).toFixed(1)} GB`;

// As máquinas onde correm as apps e os addons dos projetos: a "local" (a
// do launcher) e as que entram com "basestack node join" (agente).
export default function NodesClient() {
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [join, setJoin] = useState<NodeJoinToken | null>(null);
  const [check, setCheck] = useState<{ node: string; result: NodeCheck | null } | null>(null);

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

  async function addNode() {
    setError(null);
    try {
      setJoin(await api<NodeJoinToken>("nodes/join-tokens", { method: "POST" }));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function runCheck(n: NodeView) {
    setCheck({ node: n.name, result: null });
    try {
      setCheck({ node: n.name, result: await api<NodeCheck>(`nodes/${encodeURIComponent(n.name)}/check`, { method: "POST" }) });
    } catch (e) {
      setCheck({ node: n.name, result: { ok: false, output: "", error: (e as Error).message } });
    }
  }

  async function remove(n: NodeView) {
    if (!confirm(`Remover o nó ${n.name}? O agente deixa de conseguir ligar-se (na máquina: basestack node leave).`)) return;
    setBusy(n.name);
    try {
      await api(`nodes/${encodeURIComponent(n.name)}`, { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <PageHeader
        title="Nós"
        description="As máquinas onde correm as apps e os addons dos projetos."
        actions={
          <button className="primary" onClick={addNode}>
            Adicionar nó
          </button>
        }
      />
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
                        {n.os || "?"}/{n.arch || "?"}
                      </code>
                      <div className="muted">
                        Docker {n.dockerVersion || "?"}
                        {n.kind === "agent" && ` · agente ${n.agentVersion || "?"}`}
                      </div>
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
                    <td className="row" style={{ gap: 6, flexWrap: "nowrap" }}>
                      <button className="secondary" disabled={!n.online} onClick={() => runCheck(n)} title="Corre um container pequeno no nó">
                        Testar
                      </button>
                      {n.schedulable ? (
                        <button className="secondary" disabled={busy === n.name} onClick={() => setSchedulable(n, false)}>
                          Manutenção
                        </button>
                      ) : (
                        <button className="primary" disabled={busy === n.name} onClick={() => setSchedulable(n, true)}>
                          Reativar
                        </button>
                      )}
                      {n.kind === "agent" && (
                        <button className="danger" disabled={busy === n.name} onClick={() => remove(n)}>
                          Remover
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
          réplicas novas; as que lá estão continuam a correr. Os nós remotos ainda não recebem apps: falta a rede entre
          nós.
        </p>
      </div>

      {join && <JoinModal join={join} onClose={() => { setJoin(null); load(); }} />}

      {check && (
        <Modal title={`Testar ${check.node}`} onClose={() => setCheck(null)}>
          {!check.result ? (
            <p className="muted">A correr um container de teste no nó...</p>
          ) : (
            <>
              {check.result.ok ? <div className="notice">O nó está utilizável.</div> : <div className="error">{check.result.error}</div>}
              {check.result.output && <pre className="generated">{check.result.output}</pre>}
            </>
          )}
          <button className="secondary" onClick={() => setCheck(null)}>
            Fechar
          </button>
        </Modal>
      )}
    </>
  );
}

// O comando do join é comprido (URL, token e impressão digital da CA): vai
// num bloco que parte a linha, com o botão de copiar por baixo.
function JoinModal({ join, onClose }: { join: NodeJoinToken; onClose: () => void }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(join.command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Sem acesso ao clipboard: o comando continua visível para copiar à mão.
    }
  }

  return (
    <Modal title="Adicionar nó" onClose={onClose} width={680}>
      <ol style={{ paddingLeft: "1.2rem", lineHeight: 1.6 }}>
        <li>
          Numa máquina Linux com Docker, instala o <code>basestack</code>.
        </li>
        <li>Corre lá este comando:</li>
      </ol>
      <pre className="generated" style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
        {join.command}
      </pre>
      <div className="row" style={{ alignItems: "center", gap: 12 }}>
        <button className="primary" onClick={copy}>
          {copied ? "Copiado" : "Copiar comando"}
        </button>
        <span className="muted">
          Serve uma vez e expira às {new Date(join.expiresAt).toLocaleTimeString("pt-PT", { hour: "2-digit", minute: "2-digit" })}.
        </span>
      </div>
      <p className="muted" style={{ marginTop: 16 }}>
        O <code>--ca</code> é a impressão digital da CA desta plataforma: o nó só se liga a quem a tem. O comando pede{" "}
        <code>sudo</code> para o Docker dessa máquina confiar no registry e arranca o agente (container{" "}
        <code>basestack-agent</code>), que se liga para cá -- o nó não abre portas. Quando terminar, o nó aparece aqui.
      </p>
      <button className="secondary" onClick={onClose}>
        Fechar
      </button>
    </Modal>
  );
}
