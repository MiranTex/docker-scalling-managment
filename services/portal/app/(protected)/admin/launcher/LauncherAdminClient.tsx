"use client";

import { useEffect, useMemo, useState } from "react";
import type { LauncherInstance, ReplicaSummary } from "@/lib/launcherClient";
import type { ServiceTemplate } from "@/lib/templatesAdminClient";
import ConfirmModal from "../database/ConfirmModal";
import NetworkPicker from "./NetworkPicker";

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
  exposedHost?: string;
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
    exposedHost: i.exposedHost,
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
    exposedHost: r.exposedHost,
  };
}

export default function LauncherAdminClient() {
  const [instances, setInstances] = useState<LauncherInstance[] | null>(null);
  const [replicas, setReplicas] = useState<ReplicaSummary[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("active");

  const [templates, setTemplates] = useState<ServiceTemplate[] | null>(null);
  const [templateName, setTemplateName] = useState("");
  const [network, setNetwork] = useState("");
  const [exposeAs, setExposeAs] = useState("");
  const [exposePort, setExposePort] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

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

  async function loadTemplates() {
    try {
      const res = await fetch("/api/admin/templates");
      if (!res.ok) return;
      const data: ServiceTemplate[] = await res.json();
      setTemplates(data);
      if (!templateName && data.length > 0) setTemplateName(data[0].name);
    } catch {
      // A lista de modelos é só para preencher o <select> -- se falhar,
      // quem quiser lançar uma instância ainda pode escrever o nome à mão.
    }
  }

  useEffect(() => {
    loadInstances();
    loadTemplates();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const rows = useMemo(() => {
    if (!instances || !replicas) return null;
    const all = [...instances.map(instanceToRow), ...replicas.map(replicaToRow)];
    if (statusFilter === "all") return all;
    return all.filter((row) => (statusFilter === "active" ? row.active : !row.active));
  }, [instances, replicas, statusFilter]);

  async function handleCreate() {
    setFormError(null);
    if (!templateName.trim()) {
      setFormError("nome do modelo é obrigatório");
      return;
    }
    if (exposeAs.trim() && !exposePort.trim()) {
      setFormError("porta interna é obrigatória para expor a instância");
      return;
    }
    setSaving(true);
    try {
      const res = await fetch("/api/admin/launcher", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          templateName,
          kind: "solo",
          ...(network.trim() ? { network: network.trim() } : {}),
          ...(exposeAs.trim() ? { exposeAs: exposeAs.trim(), exposePort: Number(exposePort) } : {}),
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setFormError(data.message ?? "erro ao lançar instância");
        return;
      }
      await loadInstances();
    } catch {
      setFormError("erro ao lançar instância");
    } finally {
      setSaving(false);
    }
  }

  async function handleAction() {
    if (!actionTarget) return;
    const { containerId, action } = actionTarget;
    setActionTarget(null);
    setActionError(null);
    try {
      const res = await fetch(`/api/admin/launcher/containers/${encodeURIComponent(containerId)}${action === "stop" ? "/stop" : ""}`, {
        method: action === "stop" ? "POST" : "DELETE",
      });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setActionError(data.message ?? "erro ao aplicar ação no container");
        return;
      }
      await loadInstances();
    } catch {
      setActionError("erro ao aplicar ação no container");
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Instâncias</h1>
        <p className="muted">
          Tudo o que corre nesta plataforma fora de um autoscaler-group: instâncias <code>solo</code>{" "}
          lançadas aqui (aplicação sem autoscaler à volta, <code>env</code> já resolvido contra o
          secretsadmin), o processo de cada <code>group</code> (lançado em{" "}
          <a href="/admin/autoscaler">/admin/autoscaler</a>, "Criar grupo") e as suas{" "}
          <code>replica</code>s (as réplicas de aplicação que esse group cria por conta própria).
          Parar/eliminar um <code>group</code> inteiro é sempre feito a partir de{" "}
          <a href="/admin/autoscaler">/admin/autoscaler</a>, nunca daqui -- mas as suas réplicas
          podem ser paradas ou eliminadas individualmente aqui, tal como uma instância solo.
        </p>
        {listError && <div className="error">{listError}</div>}
        {actionError && <div className="error">{actionError}</div>}

        <label htmlFor="status-filter">Mostrar</label>
        <select id="status-filter" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value as StatusFilter)}>
          <option value="active">Ativas</option>
          <option value="stopped">Paradas</option>
          <option value="all">Todas</option>
        </select>

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
                      <a href={`http://${row.exposedHost}`} target="_blank" rel="noreferrer">
                        {row.exposedHost}
                      </a>
                    ) : (
                      "--"
                    )}
                  </td>
                  <td>
                    {row.kind === "group" ? (
                      row.active && (
                        <a href="/admin/autoscaler" className="muted">
                          gerir em /admin/autoscaler
                        </a>
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

      <div className="card wide">
        <h1>Lançar nova instância</h1>
        {formError && <div className="error">{formError}</div>}

        <label htmlFor="instance-template">Modelo</label>
        {templates && templates.length > 0 ? (
          <select
            id="instance-template"
            value={templateName}
            onChange={(e) => setTemplateName(e.target.value)}
          >
            {templates.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
        ) : (
          <input
            id="instance-template"
            type="text"
            value={templateName}
            onChange={(e) => setTemplateName(e.target.value)}
            placeholder="nome do modelo (services/templatesadmin)"
          />
        )}

        <label htmlFor="instance-network">Rede Docker (opcional, substitui a do modelo)</label>
        <NetworkPicker id="instance-network" value={network} onChange={setNetwork} />
        <p className="muted">
          Deixa em branco para usar a rede definida no modelo. Se o container ficar "inatingível"
          depois de lançado, é normalmente porque nem o modelo nem este campo têm a rede certa (o
          Docker usa "bridge" por defeito, isolada do launcher/portal).
        </p>

        <label htmlFor="instance-expose-as">Expor publicamente como (opcional)</label>
        <input
          id="instance-expose-as"
          type="text"
          value={exposeAs}
          onChange={(e) => setExposeAs(e.target.value)}
          placeholder="minha-app"
        />
        <label htmlFor="instance-expose-port">Porta interna (obrigatória se expor publicamente)</label>
        <input
          id="instance-expose-port"
          type="number"
          value={exposePort}
          onChange={(e) => setExposePort(e.target.value)}
          placeholder="80"
        />
        <p className="muted">
          Deixa "expor publicamente" em branco para a instância continuar só na rede interna, como
          hoje. Preenchido, fica acessível em <code>http://{exposeAs.trim() || "..."}.PUBLIC_BASE_DOMAIN</code>{" "}
          através do gateway único (Traefik) -- nunca publica porta nenhuma no host. A porta interna é
          a que a APLICAÇÃO escuta lá dentro do container, não uma porta do host.
        </p>

        <div className="row">
          <button className="secondary" disabled={saving} onClick={handleCreate}>
            {saving ? "A lançar..." : "Lançar"}
          </button>
        </div>
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
