"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { InstanceSummary, PolicyDTO } from "@/lib/autoscalerAdminClient";
import ConfirmModal from "../../database/ConfirmModal";

function formatDate(iso?: string): string {
  if (!iso) return "-";
  return new Date(iso).toLocaleString("pt-PT");
}

export default function AutoscalerDetailClient({ id }: { id: string }) {
  const router = useRouter();
  const [instance, setInstance] = useState<InstanceSummary | null>(null);
  const [notFound, setNotFound] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);

  const [policyDraft, setPolicyDraft] = useState<PolicyDTO>({});
  const [policyError, setPolicyError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [restarting, setRestarting] = useState(false);
  const [restartError, setRestartError] = useState<string | null>(null);
  const [confirmRestart, setConfirmRestart] = useState(false);

  const [addingReplica, setAddingReplica] = useState(false);
  const [replicaError, setReplicaError] = useState<string | null>(null);
  const [killTarget, setKillTarget] = useState<string | null>(null);
  const [killingReplica, setKillingReplica] = useState(false);

  const [stoppingGroup, setStoppingGroup] = useState(false);
  const [stopGroupError, setStopGroupError] = useState<string | null>(null);
  const [confirmStopGroup, setConfirmStopGroup] = useState(false);

  async function loadInstance() {
    try {
      const res = await fetch(`/api/admin/autoscaler/${encodeURIComponent(id)}`);
      if (res.status === 404) {
        setNotFound(true);
        return;
      }
      if (!res.ok) {
        setLoadError(`erro ${res.status} a carregar instância`);
        return;
      }
      const data: InstanceSummary = await res.json();
      setInstance(data);
      setLoadError(null);
      if (data.status?.policy) setPolicyDraft(data.status.policy);
    } catch {
      setLoadError("erro a carregar instância");
    }
  }

  useEffect(() => {
    loadInstance();
    const interval = setInterval(loadInstance, 5000);
    return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);

  async function handleSavePolicy() {
    setSaving(true);
    setPolicyError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${encodeURIComponent(id)}/policy`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(policyDraft),
      });
      const data = await res.json();
      if (!res.ok) {
        setPolicyError(data.message ?? "erro ao atualizar policy");
        return;
      }
      await loadInstance();
    } catch {
      setPolicyError("erro ao atualizar policy");
    } finally {
      setSaving(false);
    }
  }

  async function handleRestart() {
    setConfirmRestart(false);
    setRestarting(true);
    setRestartError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${encodeURIComponent(id)}/restart`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        setRestartError(data.message ?? "erro ao reiniciar");
        return;
      }
      await loadInstance();
    } catch {
      setRestartError("erro ao reiniciar");
    } finally {
      setRestarting(false);
    }
  }

  async function handleStopGroup() {
    setConfirmStopGroup(false);
    setStoppingGroup(true);
    setStopGroupError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setStopGroupError(data.message ?? "erro ao parar grupo");
        return;
      }
      router.push("/admin/autoscaler");
    } catch {
      setStopGroupError("erro ao parar grupo");
    } finally {
      setStoppingGroup(false);
    }
  }

  async function handleAddReplica() {
    setAddingReplica(true);
    setReplicaError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${encodeURIComponent(id)}/replicas`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        setReplicaError(data.message ?? "erro ao lançar réplica");
        return;
      }
      await loadInstance();
    } catch {
      setReplicaError("erro ao lançar réplica");
    } finally {
      setAddingReplica(false);
    }
  }

  async function handleRemoveReplica() {
    if (!killTarget) return;
    const target = killTarget;
    setKillTarget(null);
    setKillingReplica(true);
    setReplicaError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${encodeURIComponent(id)}/replicas/${encodeURIComponent(target)}`, {
        method: "DELETE",
      });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setReplicaError(data.message ?? "erro ao matar réplica");
        return;
      }
      await loadInstance();
    } catch {
      setReplicaError("erro ao matar réplica");
    } finally {
      setKillingReplica(false);
    }
  }

  if (notFound) {
    return (
      <div className="card wide">
        <h1>Autoscaler-group</h1>
        <p className="error">
          Instância desconhecida -- já não existe, ou o launcher não consegue descobri-la.
        </p>
        <Link href="/admin/autoscaler" className="muted">
          ← Voltar à lista
        </Link>
      </div>
    );
  }

  if (!instance) {
    return (
      <div className="card wide">
        <h1>Autoscaler-group</h1>
        {loadError && <div className="error">{loadError}</div>}
        <p className="muted">A carregar...</p>
      </div>
    );
  }

  const status = instance.status;

  return (
    <>
      <div className="card wide">
        <div className="row">
          <h1>
            {instance.label}
            {status && <> -- {status.target_service}</>}
          </h1>
          <Link href="/admin/autoscaler" className="muted">
            ← Voltar à lista
          </Link>
        </div>
        {loadError && <div className="error">{loadError}</div>}
        {instance.error && <div className="error">{instance.error}</div>}

        {status && (
          <>
            <table>
              <tbody>
                <tr>
                  <th>Rede</th>
                  <td>
                    <code>{instance.network || "-"}</code>
                  </td>
                </tr>
                <tr>
                  <th>Público</th>
                  <td>
                    {instance.exposedHost ? (
                      <a href={`${instance.exposedScheme || "http"}://${instance.exposedHost}`} target="_blank" rel="noreferrer">
                        {instance.exposedHost}
                      </a>
                    ) : (
                      "-- (só na rede interna)"
                    )}
                  </td>
                </tr>
                <tr>
                  <th>Réplicas</th>
                  <td>{status.replica_count}</td>
                </tr>
                <tr>
                  <th>Uptime</th>
                  <td>{Math.round(status.uptime_seconds / 60)} min</td>
                </tr>
                <tr>
                  <th>Launch template</th>
                  <td>
                    {status.launch_template.path}
                    {status.launch_template.modified_at && (
                      <span className="muted"> (alterado {formatDate(status.launch_template.modified_at)})</span>
                    )}
                  </td>
                </tr>
                <tr>
                  <th>Última ação</th>
                  <td>
                    {status.last_scale_action
                      ? `${status.last_scale_action.action} em ${formatDate(status.last_scale_action.at)} -- ${status.last_scale_action.reason}`
                      : "nenhuma ainda"}
                  </td>
                </tr>
              </tbody>
            </table>

            {replicaError && <div className="error">{replicaError}</div>}
            <div className="row">
              <button className="secondary" disabled={addingReplica} onClick={handleAddReplica}>
                {addingReplica ? "A lançar..." : "Lançar réplica"}
              </button>
            </div>

            {status.replicas.length > 0 && (
              <table>
                <thead>
                  <tr>
                    <th>Container</th>
                    <th>Nome</th>
                    <th>Estado</th>
                    <th>CPU %</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {status.replicas.map((r) => (
                    <tr key={r.container_id}>
                      <td>
                        <code>{r.container_id.slice(0, 12)}</code>
                      </td>
                      <td>{r.name}</td>
                      <td>
                        <span className={`badge ${r.state === "running" ? "active" : "revoked"}`}>{r.status}</span>
                      </td>
                      <td>{r.cpu_percent.toFixed(1)}</td>
                      <td>
                        <button className="danger" disabled={killingReplica} onClick={() => setKillTarget(r.container_id)}>
                          Matar
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </div>

      {status && (
        <>
          <div className="card wide">
            <h1>Policy de scaling</h1>
            <p className="muted">
              Aplicada a quente, sem restart -- ver services/autoscaler/internal/scaler.Evaluator.
              O launch template (imagem/env/binds) não é editável aqui; mudar isso exige editar o
              ficheiro e reiniciar (abaixo).
            </p>
            {policyError && <div className="error">{policyError}</div>}

            <label htmlFor="min-replicas">Min réplicas</label>
            <input
              id="min-replicas"
              type="number"
              min={0}
              value={policyDraft.min_replicas ?? 0}
              onChange={(e) => setPolicyDraft({ ...policyDraft, min_replicas: Number(e.target.value) })}
            />
            <label htmlFor="max-replicas">Max réplicas</label>
            <input
              id="max-replicas"
              type="number"
              min={1}
              value={policyDraft.max_replicas ?? 1}
              onChange={(e) => setPolicyDraft({ ...policyDraft, max_replicas: Number(e.target.value) })}
            />
            <label htmlFor="cpu-up">CPU scale up (%)</label>
            <input
              id="cpu-up"
              type="number"
              value={policyDraft.cpu_scale_up_percent ?? 0}
              onChange={(e) => setPolicyDraft({ ...policyDraft, cpu_scale_up_percent: Number(e.target.value) })}
            />
            <label htmlFor="cpu-down">CPU scale down (%)</label>
            <input
              id="cpu-down"
              type="number"
              value={policyDraft.cpu_scale_down_percent ?? 0}
              onChange={(e) => setPolicyDraft({ ...policyDraft, cpu_scale_down_percent: Number(e.target.value) })}
            />
            <label htmlFor="sustained-ticks">Ticks sustentados</label>
            <input
              id="sustained-ticks"
              type="number"
              min={0}
              value={policyDraft.sustained_ticks ?? 0}
              onChange={(e) => setPolicyDraft({ ...policyDraft, sustained_ticks: Number(e.target.value) })}
            />
            <label htmlFor="cooldown">Cooldown (segundos)</label>
            <input
              id="cooldown"
              type="number"
              min={0}
              value={policyDraft.cooldown_seconds ?? 0}
              onChange={(e) => setPolicyDraft({ ...policyDraft, cooldown_seconds: Number(e.target.value) })}
            />

            <div className="row">
              <button className="secondary" disabled={saving} onClick={handleSavePolicy}>
                {saving ? "A guardar..." : "Guardar policy"}
              </button>
            </div>
          </div>

          <div className="card wide">
            <h1>Restart</h1>
            <p className="error">
              Termina todas as réplicas geridas por esta instância e reconstrói a partir de
              min_replicas, usando o launch template como estiver em disco neste momento -- é assim
              que se aplica uma mudança de imagem/env/binds. Não há "cancelar" a meio.
            </p>
            {restartError && <div className="error">{restartError}</div>}
            <button className="danger" disabled={restarting} onClick={() => setConfirmRestart(true)}>
              {restarting ? "A reiniciar..." : "Reiniciar"}
            </button>
          </div>

          <div className="card wide">
            <h1>Parar grupo</h1>
            <p className="error">
              Para e remove o container deste autoscaler-group -- ele próprio remove as suas
              réplicas antes de terminar (mesmo caminho do shutdown normal). Ao contrário do
              restart, não volta a subir sozinho depois. Esta é a única ação desta página que
              termina o próprio group, não uma réplica dele -- por isso não existe equivalente em
              /admin/launcher.
            </p>
            {stopGroupError && <div className="error">{stopGroupError}</div>}
            <button className="danger" disabled={stoppingGroup} onClick={() => setConfirmStopGroup(true)}>
              {stoppingGroup ? "A parar..." : "Parar grupo"}
            </button>
          </div>
        </>
      )}

      {confirmRestart && status && (
        <ConfirmModal
          title="Confirmar restart"
          message={`Isto vai terminar e reconstruir todas as réplicas de "${status.target_service}" geridas por ${instance.label}.`}
          expectedText={status.target_service}
          confirmLabel="Reiniciar"
          onConfirm={handleRestart}
          onCancel={() => setConfirmRestart(false)}
        />
      )}

      {killTarget && (
        <ConfirmModal
          title="Confirmar remoção"
          message={`Isto para e remove imediatamente a réplica ${killTarget.slice(0, 12)}.`}
          expectedText={killTarget.slice(0, 12)}
          confirmLabel="Matar"
          onConfirm={handleRemoveReplica}
          onCancel={() => setKillTarget(null)}
        />
      )}

      {confirmStopGroup && status && (
        <ConfirmModal
          title="Confirmar paragem do grupo"
          message={`Isto termina o autoscaler-group "${instance.label}" (serviço "${status.target_service}") e todas as réplicas que ele gere -- não volta a subir sozinho.`}
          expectedText={status.target_service}
          confirmLabel="Parar grupo"
          onConfirm={handleStopGroup}
          onCancel={() => setConfirmStopGroup(false)}
        />
      )}
    </>
  );
}
