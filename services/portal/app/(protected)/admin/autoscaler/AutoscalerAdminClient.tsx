"use client";

import { useEffect, useState } from "react";
import type { AutoscalerStatus, PolicyDTO } from "@/lib/autoscalerAdminClient";
import ConfirmModal from "../database/ConfirmModal";

type InstanceSummary = {
  id: string;
  label: string;
  url: string;
  status?: AutoscalerStatus;
  error?: string;
};

type ListResponse = { instances: InstanceSummary[] };

function formatDate(iso?: string): string {
  if (!iso) return "-";
  return new Date(iso).toLocaleString("pt-PT");
}

export default function AutoscalerAdminClient() {
  const [instances, setInstances] = useState<InstanceSummary[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const [policyDraft, setPolicyDraft] = useState<PolicyDTO>({});
  const [policyError, setPolicyError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [restarting, setRestarting] = useState(false);
  const [restartError, setRestartError] = useState<string | null>(null);
  const [confirmRestart, setConfirmRestart] = useState(false);

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

  const selected = instances?.find((i) => i.id === selectedId) ?? null;

  useEffect(() => {
    if (selected?.status?.policy) {
      setPolicyDraft(selected.status.policy);
      setPolicyError(null);
    }
  }, [selected?.id, selected?.status?.policy]);

  async function handleSavePolicy() {
    if (!selected) return;
    setSaving(true);
    setPolicyError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${selected.id}/policy`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(policyDraft),
      });
      const data = await res.json();
      if (!res.ok) {
        setPolicyError(data.message ?? "erro ao atualizar policy");
        return;
      }
      await loadInstances();
    } catch {
      setPolicyError("erro ao atualizar policy");
    } finally {
      setSaving(false);
    }
  }

  async function handleRestart() {
    if (!selected) return;
    setConfirmRestart(false);
    setRestarting(true);
    setRestartError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${selected.id}/restart`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        setRestartError(data.message ?? "erro ao reiniciar");
        return;
      }
      await loadInstances();
    } catch {
      setRestartError("erro ao reiniciar");
    } finally {
      setRestarting(false);
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Autoscalers</h1>
        <p className="muted">
          Cada linha é uma instância do autoscaler group (um serviço gerido, um host) configurada
          para este ambiente. Ver services/autoscaler/README.md.
        </p>
        {listError && <div className="error">{listError}</div>}
        {!instances ? (
          <p className="muted">A carregar...</p>
        ) : instances.length === 0 ? (
          <p className="muted">Nenhum autoscaler configurado para este ambiente.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Instância</th>
                <th>Serviço</th>
                <th>Réplicas</th>
                <th>Estado</th>
              </tr>
            </thead>
            <tbody>
              {instances.map((i) => (
                <tr
                  key={i.id}
                  onClick={() => setSelectedId(i.id)}
                  style={{ cursor: "pointer", fontWeight: i.id === selectedId ? "bold" : "normal" }}
                >
                  <td>{i.label}</td>
                  <td>{i.status?.target_service ?? "-"}</td>
                  <td>
                    {i.status ? `${i.status.replica_count} (min ${i.status.policy.min_replicas} / max ${i.status.policy.max_replicas})` : "-"}
                  </td>
                  <td className={i.error ? "error" : ""}>{i.error ?? "ok"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {selected && selected.status && (
        <>
          <div className="card wide">
            <h1>
              {selected.label} -- {selected.status.target_service}
            </h1>

            <table>
              <tbody>
                <tr>
                  <th>Réplicas</th>
                  <td>{selected.status.replica_count}</td>
                </tr>
                <tr>
                  <th>Uptime</th>
                  <td>{Math.round(selected.status.uptime_seconds / 60)} min</td>
                </tr>
                <tr>
                  <th>Launch template</th>
                  <td>
                    {selected.status.launch_template.path}
                    {selected.status.launch_template.modified_at && (
                      <span className="muted"> (alterado {formatDate(selected.status.launch_template.modified_at)})</span>
                    )}
                  </td>
                </tr>
                <tr>
                  <th>Última ação</th>
                  <td>
                    {selected.status.last_scale_action
                      ? `${selected.status.last_scale_action.action} em ${formatDate(selected.status.last_scale_action.at)} -- ${selected.status.last_scale_action.reason}`
                      : "nenhuma ainda"}
                  </td>
                </tr>
              </tbody>
            </table>

            {selected.status.replicas.length > 0 && (
              <table>
                <thead>
                  <tr>
                    <th>Container</th>
                    <th>Nome</th>
                    <th>Estado</th>
                    <th>CPU %</th>
                  </tr>
                </thead>
                <tbody>
                  {selected.status.replicas.map((r) => (
                    <tr key={r.container_id}>
                      <td>{r.container_id.slice(0, 12)}</td>
                      <td>{r.name}</td>
                      <td>{r.status}</td>
                      <td>{r.cpu_percent.toFixed(1)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

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
        </>
      )}

      {confirmRestart && selected && (
        <ConfirmModal
          title="Confirmar restart"
          message={`Isto vai terminar e reconstruir todas as réplicas de "${selected.status?.target_service}" geridas por ${selected.label}.`}
          expectedText={selected.status?.target_service ?? selected.label}
          confirmLabel="Reiniciar"
          onConfirm={handleRestart}
          onCancel={() => setConfirmRestart(false)}
        />
      )}
    </>
  );
}
