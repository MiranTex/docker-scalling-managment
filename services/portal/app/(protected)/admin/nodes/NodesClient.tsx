"use client";

import Loading from "@/components/Skeleton";
import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import PageHeader from "@/components/PageHeader";
import { api, type NodeCheck, type NodeJoinToken, type NodeView } from "@/lib/platformClient";
import { formatLabels, parseLabels } from "@/lib/labels";

const gb = (mb: number) => `${(mb / 1024).toFixed(1)} GB`;

// As máquinas onde correm as apps e os addons dos projetos: a "local" (a
// do launcher) e as que entram com "basestack node join" (agente).
export default function NodesClient() {
  const [nodes, setNodes] = useState<NodeView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [join, setJoin] = useState<NodeJoinToken | null>(null);
  const [check, setCheck] = useState<{ node: string; result: NodeCheck | null } | null>(null);
  const [editLabels, setEditLabels] = useState<{ node: NodeView; text: string; error: string | null } | null>(null);

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
    const t = setInterval(load, 5000);
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

  async function saveLabels(e: React.FormEvent) {
    e.preventDefault();
    if (!editLabels) return;
    try {
      const labels = parseLabels(editLabels.text);
      await api(`nodes/${encodeURIComponent(editLabels.node.name)}`, { method: "PATCH", body: JSON.stringify({ labels }) });
      setEditLabels(null);
      await load();
    } catch (err) {
      setEditLabels({ ...editLabels, error: (err as Error).message });
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

  async function drain(n: NodeView) {
    if (!confirm(`Esvaziar ${n.name}? Fica em manutenção e as ${n.replicas} réplica(s) dele mudam-se para outros nós, uma a uma. Os addons ficam.`)) return;
    try {
      await api(`nodes/${encodeURIComponent(n.name)}/drain`, { method: "POST" });
      await load();
    } catch (e) {
      setError((e as Error).message);
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
          <Loading />
        ) : nodes.length === 0 ? (
          <p className="muted">Ainda nenhum nó registado -- o launcher regista a máquina local ao arrancar.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nó</th>
                <th>Estado</th>
                <th>Plataforma</th>
                <th>Rede das apps</th>
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
                      {(Object.keys(n.autoLabels ?? {}).length > 0 || Object.keys(n.labels ?? {}).length > 0) && (
                        <div style={{ display: "flex", gap: 4, flexWrap: "wrap", marginTop: 4 }}>
                          {Object.entries(n.autoLabels ?? {})
                            .filter(([k]) => !(k in (n.labels ?? {})))
                            .map(([k, v]) => (
                              <span key={k} className="badge label" title="Detetada pela plataforma" style={{ fontStyle: "italic" }}>
                                {k}={v}
                              </span>
                            ))}
                          {Object.entries(n.labels ?? {}).map(([k, v]) => (
                            <span key={k} className="badge label">
                              {k}={v}
                            </span>
                          ))}
                        </div>
                      )}
                      {n.drain && !n.draining && <div className="muted">{n.drain}</div>}
                      {n.address && (
                        <div className="muted">
                          <code>{n.address}</code>
                        </div>
                      )}
                    </td>
                    <td>
                      {n.status === "perdido" ? (
                        <span className="badge failed">perdido</span>
                      ) : !n.online ? (
                        <span className="badge pending" title="As réplicas dele continuam a contar até 60s sem sinal">
                          {n.status}
                        </span>
                      ) : n.draining ? (
                        <span className="badge pending">a esvaziar</span>
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
                      {n.agentOutdated && (
                        <div>
                          <span className="badge pending" title={`Na máquina: basestack upgrade (fica com ${n.agentLatest})`}>
                            agente desatualizado
                          </span>
                        </div>
                      )}
                    </td>
                    <td>
                      {n.network === "local" ? (
                        <span className="muted">local</span>
                      ) : n.network === "cluster" ? (
                        <span className="badge active">cluster</span>
                      ) : (
                        <span className="muted">{n.network}</span>
                      )}
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
                      <button
                        className="secondary"
                        onClick={() => setEditLabels({ node: n, text: formatLabels(n.labels, "\n"), error: null })}
                        title="As réplicas de serviços que exigem etiquetas só vão para nós que as tenham"
                      >
                        Etiquetas
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
                      <button
                        className="secondary"
                        disabled={!n.online || n.draining || n.replicas === 0}
                        onClick={() => drain(n)}
                        title="Muda as réplicas deste nó para outros, uma a uma, sem cortar o serviço"
                      >
                        Esvaziar
                      </button>
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
          réplicas novas; as que lá estão continuam a correr. Um nó remoto só recebe apps com a plataforma em modo cluster
          (rede overlay entre os nós).
        </p>
      </div>

      {join && <JoinModal join={join} onClose={() => { setJoin(null); load(); }} />}

      {editLabels && (
        <Modal title={`Etiquetas de ${editLabels.node.name}`} onClose={() => setEditLabels(null)}>
          <p className="muted">
            Um serviço que exija etiquetas (na Configuração dele) só recebe réplicas em nós que tenham todas. Mudar aqui não
            move as réplicas que já correm.
          </p>
          {Object.keys(editLabels.node.autoLabels ?? {}).length > 0 && (
            <p>
              Detetadas pela plataforma: <code>{formatLabels(editLabels.node.autoLabels)}</code>
            </p>
          )}
          {editLabels.error && <div className="error">{editLabels.error}</div>}
          <form onSubmit={saveLabels}>
            <label htmlFor="node-labels">Uma por linha, chave=valor (ex: disco=ssd)</label>
            <textarea
              id="node-labels"
              rows={5}
              value={editLabels.text}
              onChange={(e) => setEditLabels({ ...editLabels, text: e.target.value })}
            />
            <div className="row">
              <button className="primary" type="submit">
                Guardar
              </button>
              <button className="secondary" type="button" onClick={() => setEditLabels(null)}>
                Cancelar
              </button>
            </div>
          </form>
        </Modal>
      )}

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
