"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import type { ServiceTemplate } from "@/lib/templatesAdminClient";
import type { LauncherInstance } from "@/lib/launcherClient";
import NetworkPicker from "../../launcher/NetworkPicker";

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

export default function CreateGroupClient() {
  const router = useRouter();
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

  return (
    <div className="card wide">
      <div className="row">
        <h1>Criar grupo</h1>
        <Link href="/admin/autoscaler" className="muted">
          ← Voltar à lista
        </Link>
      </div>
      <p className="muted">
        Lança um autoscaler-group inteiro a partir de um modelo do templatesadmin (imagem, env,
        binds, rede) -- o group recém-criado passa a gerir as suas próprias réplicas, pedindo cada
        uma ao launcher (ver services/launcher). Réplicas mín./máx. e thresholds de CPU são
        decididos aqui, não no modelo.
      </p>
      <p className="muted">
        Assim que o container arrancar, aparece sozinho na lista -- descoberta em tempo real
        contra o Docker (ver services/launcher, GET /v1/groups), não precisa de nenhum registo
        manual.
      </p>
      {groupError && <div className="error">{groupError}</div>}
      {groupCreated && (
        <div className="notice">
          Grupo lançado: instância <code>{groupCreated.id}</code>, container{" "}
          <code>{groupCreated.containerId.slice(0, 12)}</code>, estado {groupCreated.status}. {" "}
          <Link href={`/admin/autoscaler/${groupCreated.containerId}`}>Ver detalhes →</Link>
        </div>
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
        <button className="secondary" onClick={() => router.push("/admin/autoscaler")}>
          Cancelar
        </button>
      </div>
    </div>
  );
}
