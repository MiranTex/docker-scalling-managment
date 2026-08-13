"use client";

import { useEffect, useState } from "react";
import type { AutoscalerStatus, PolicyDTO } from "@/lib/autoscalerAdminClient";
import type { ServiceTemplate } from "@/lib/templatesAdminClient";
import type { LauncherInstance } from "@/lib/launcherClient";
import ConfirmModal from "../database/ConfirmModal";
import NetworkPicker from "../launcher/NetworkPicker";

type InstanceSummary = {
  id: string;
  label: string;
  url: string;
  network: string;
  status?: AutoscalerStatus;
  error?: string;
};

type ListResponse = { instances: InstanceSummary[] };

const EMPTY_GROUP_FORM = {
  templateName: "",
  targetService: "",
  network: "",
  backendPort: "80",
  minReplicas: "1",
  maxReplicas: "3",
  cpuScaleUpPercent: "50",
  cpuScaleDownPercent: "20",
  replicaAuthToken: "",
};

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

  const [addingReplica, setAddingReplica] = useState(false);
  const [replicaError, setReplicaError] = useState<string | null>(null);
  const [killTarget, setKillTarget] = useState<string | null>(null);
  const [killingReplica, setKillingReplica] = useState(false);

  const [stoppingGroup, setStoppingGroup] = useState(false);
  const [stopGroupError, setStopGroupError] = useState<string | null>(null);
  const [confirmStopGroup, setConfirmStopGroup] = useState(false);

  const [templates, setTemplates] = useState<ServiceTemplate[] | null>(null);
  const [groupForm, setGroupForm] = useState(EMPTY_GROUP_FORM);
  const [creatingGroup, setCreatingGroup] = useState(false);
  const [groupError, setGroupError] = useState<string | null>(null);
  const [groupCreated, setGroupCreated] = useState<LauncherInstance | null>(null);

  async function loadTemplates() {
    try {
      const res = await fetch("/api/admin/templates");
      if (!res.ok) return;
      const data: ServiceTemplate[] = await res.json();
      setTemplates(data);
      if (!groupForm.templateName && data.length > 0) {
        setGroupForm((f) => ({ ...f, templateName: data[0].name }));
      }
    } catch {
      // A lista de modelos é só para preencher o <select> -- se falhar,
      // ainda é possível escrever o nome à mão.
    }
  }

  useEffect(() => {
    loadTemplates();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function handleCreateGroup() {
    setGroupError(null);
    setGroupCreated(null);
    if (!groupForm.templateName.trim()) {
      setGroupError("modelo é obrigatório");
      return;
    }
    if (!groupForm.targetService.trim()) {
      setGroupError("nome do serviço (target service) é obrigatório");
      return;
    }
    setCreatingGroup(true);
    try {
      const res = await fetch("/api/admin/launcher", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          templateName: groupForm.templateName,
          kind: "group",
          targetService: groupForm.targetService.trim(),
          ...(groupForm.network.trim() ? { network: groupForm.network.trim() } : {}),
          backendPort: Number(groupForm.backendPort) || 80,
          minReplicas: Number(groupForm.minReplicas) || 0,
          maxReplicas: Number(groupForm.maxReplicas) || 1,
          cpuScaleUpPercent: Number(groupForm.cpuScaleUpPercent) || 0,
          cpuScaleDownPercent: Number(groupForm.cpuScaleDownPercent) || 0,
          ...(groupForm.replicaAuthToken ? { replicaAuthToken: groupForm.replicaAuthToken } : {}),
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setGroupError(data.message ?? "erro ao criar grupo");
        return;
      }
      setGroupCreated(data);
    } catch {
      setGroupError("erro ao criar grupo");
    } finally {
      setCreatingGroup(false);
    }
  }

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

  async function handleStopGroup() {
    if (!selected) return;
    setConfirmStopGroup(false);
    setStoppingGroup(true);
    setStopGroupError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${selected.id}`, { method: "DELETE" });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setStopGroupError(data.message ?? "erro ao parar grupo");
        return;
      }
      setSelectedId(null);
      await loadInstances();
    } catch {
      setStopGroupError("erro ao parar grupo");
    } finally {
      setStoppingGroup(false);
    }
  }

  async function handleAddReplica() {
    if (!selected) return;
    setAddingReplica(true);
    setReplicaError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${selected.id}/replicas`, { method: "POST" });
      const data = await res.json();
      if (!res.ok) {
        setReplicaError(data.message ?? "erro ao lançar réplica");
        return;
      }
      await loadInstances();
    } catch {
      setReplicaError("erro ao lançar réplica");
    } finally {
      setAddingReplica(false);
    }
  }

  async function handleRemoveReplica() {
    if (!selected || !killTarget) return;
    const target = killTarget;
    setKillTarget(null);
    setKillingReplica(true);
    setReplicaError(null);
    try {
      const res = await fetch(`/api/admin/autoscaler/${selected.id}/replicas/${encodeURIComponent(target)}`, {
        method: "DELETE",
      });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setReplicaError(data.message ?? "erro ao matar réplica");
        return;
      }
      await loadInstances();
    } catch {
      setReplicaError("erro ao matar réplica");
    } finally {
      setKillingReplica(false);
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Autoscalers</h1>
        <p className="muted">
          Cada linha é um autoscaler-group vivo, descoberto dinamicamente contra o launcher (ver
          services/launcher, GET /v1/groups) -- não uma lista configurada à mão.
        </p>
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
                    <code>{i.network || "-"}</code>
                  </td>
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

            {replicaError && <div className="error">{replicaError}</div>}
            <div className="row">
              <button className="secondary" disabled={addingReplica} onClick={handleAddReplica}>
                {addingReplica ? "A lançar..." : "Lançar réplica"}
              </button>
            </div>

            {selected.status.replicas.length > 0 && (
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
                  {selected.status.replicas.map((r) => (
                    <tr key={r.container_id}>
                      <td>{r.container_id.slice(0, 12)}</td>
                      <td>{r.name}</td>
                      <td>{r.status}</td>
                      <td>{r.cpu_percent.toFixed(1)}</td>
                      <td>
                        <button
                          className="danger"
                          disabled={killingReplica}
                          onClick={() => setKillTarget(r.container_id)}
                        >
                          Matar
                        </button>
                      </td>
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

      <div className="card wide">
        <h1>Criar grupo</h1>
        <p className="muted">
          Lança um autoscaler-group inteiro a partir de um modelo do templatesadmin (imagem, env,
          binds, rede) -- o group recém-criado passa a gerir as suas próprias réplicas, pedindo cada
          uma ao launcher (ver services/launcher). Réplicas mín./máx. e thresholds de CPU são
          decididos aqui, não no modelo.
        </p>
        <p className="muted">
          Assim que o container arrancar, aparece sozinho na tabela acima -- a lista é descoberta
          em tempo real contra o Docker (ver services/launcher, GET /v1/groups), não precisa de
          nenhum registo manual.
        </p>
        {groupError && <div className="error">{groupError}</div>}
        {groupCreated && (
          <p className="muted">
            Grupo lançado: instância <code>{groupCreated.id}</code>, container{" "}
            <code>{groupCreated.containerId.slice(0, 12)}</code>, estado {groupCreated.status}.
          </p>
        )}

        <label htmlFor="group-template">Modelo</label>
        {templates && templates.length > 0 ? (
          <select
            id="group-template"
            value={groupForm.templateName}
            onChange={(e) => setGroupForm((f) => ({ ...f, templateName: e.target.value }))}
          >
            {templates.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
        ) : (
          <input
            id="group-template"
            type="text"
            value={groupForm.templateName}
            onChange={(e) => setGroupForm((f) => ({ ...f, templateName: e.target.value }))}
            placeholder="nome do modelo (services/templatesadmin)"
          />
        )}

        <label htmlFor="group-target-service">Nome do serviço (TARGET_SERVICE)</label>
        <input
          id="group-target-service"
          type="text"
          value={groupForm.targetService}
          onChange={(e) => setGroupForm((f) => ({ ...f, targetService: e.target.value }))}
          placeholder="laravel-app"
        />

        <label htmlFor="group-network">Rede Docker (opcional, substitui a do modelo)</label>
        <NetworkPicker
          id="group-network"
          value={groupForm.network}
          onChange={(network) => setGroupForm((f) => ({ ...f, network }))}
        />
        <p className="muted">
          Tem de ser a mesma rede do launcher/portal (ex: <code>auth-net</code> no demo) para a API
          admin deste group ficar alcançável -- sem isto, um modelo sem rede definida cria o group
          na rede "bridge" do Docker, isolado, e ele aparece como "instância inalcançável".
        </p>

        <div className="row" style={{ gap: "1rem" }}>
          <div style={{ flex: 1 }}>
            <label htmlFor="group-backend-port">Porta do backend</label>
            <input
              id="group-backend-port"
              type="number"
              value={groupForm.backendPort}
              onChange={(e) => setGroupForm((f) => ({ ...f, backendPort: e.target.value }))}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label htmlFor="group-min">Réplicas mín.</label>
            <input
              id="group-min"
              type="number"
              value={groupForm.minReplicas}
              onChange={(e) => setGroupForm((f) => ({ ...f, minReplicas: e.target.value }))}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label htmlFor="group-max">Réplicas máx.</label>
            <input
              id="group-max"
              type="number"
              value={groupForm.maxReplicas}
              onChange={(e) => setGroupForm((f) => ({ ...f, maxReplicas: e.target.value }))}
            />
          </div>
        </div>

        <div className="row" style={{ gap: "1rem" }}>
          <div style={{ flex: 1 }}>
            <label htmlFor="group-cpu-up">Escalar acima de (% CPU)</label>
            <input
              id="group-cpu-up"
              type="number"
              value={groupForm.cpuScaleUpPercent}
              onChange={(e) => setGroupForm((f) => ({ ...f, cpuScaleUpPercent: e.target.value }))}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label htmlFor="group-cpu-down">Escalar abaixo de (% CPU)</label>
            <input
              id="group-cpu-down"
              type="number"
              value={groupForm.cpuScaleDownPercent}
              onChange={(e) => setGroupForm((f) => ({ ...f, cpuScaleDownPercent: e.target.value }))}
            />
          </div>
        </div>

        <label htmlFor="group-token">Refresh token do group (role &quot;service&quot;, opcional)</label>
        <input
          id="group-token"
          type="password"
          value={groupForm.replicaAuthToken}
          onChange={(e) => setGroupForm((f) => ({ ...f, replicaAuthToken: e.target.value }))}
          placeholder="emitido manualmente -- ver demo/README.md, Bootstrap"
          autoComplete="off"
        />
        <p className="muted">
          É com este token que o group recém-criado se autentica de volta contra o launcher em cada
          scale up (<code>POST /v1/replicas</code>) -- bootstrap manual, mesmo espírito do refresh
          token inicial de qualquer conta de serviço.
        </p>

        <div className="row">
          <button className="secondary" disabled={creatingGroup} onClick={handleCreateGroup}>
            {creatingGroup ? "A criar..." : "Criar grupo"}
          </button>
        </div>
      </div>

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

      {confirmStopGroup && selected && (
        <ConfirmModal
          title="Confirmar paragem do grupo"
          message={`Isto termina o autoscaler-group "${selected.label}" (serviço "${selected.status?.target_service}") e todas as réplicas que ele gere -- não volta a subir sozinho.`}
          expectedText={selected.status?.target_service ?? selected.label}
          confirmLabel="Parar grupo"
          onConfirm={handleStopGroup}
          onCancel={() => setConfirmStopGroup(false)}
        />
      )}
    </>
  );
}
