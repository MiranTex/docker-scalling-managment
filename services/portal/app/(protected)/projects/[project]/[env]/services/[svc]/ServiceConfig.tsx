"use client";

import { useEffect, useState } from "react";
import { api, servicePath, splitArgs, type Service } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";
import { formatLabels, parseLabels } from "@/lib/labels";

function toForm(s: Service, joinArgs: (a: string[]) => string) {
  return {
    port: String(s.port || ""),
    healthPath: s.healthPath,
    healthTimeoutSeconds: String(s.healthTimeoutSeconds),
    cmd: joinArgs(s.cmd),
    releaseCmd: joinArgs(s.releaseCmd),
    env: s.env.join("\n"),
    vcpu: String(s.vcpu),
    memoryMb: String(s.memoryMb),
    scalingMode: s.scalingMode,
    replicas: String(s.replicas),
    minReplicas: String(s.minReplicas),
    maxReplicas: String(s.maxReplicas),
    cpuScaleUpPercent: String(s.cpuScaleUpPercent),
    cpuScaleDownPercent: String(s.cpuScaleDownPercent),
    metricsPort: String(s.metricsPort || ""),
    metricsPath: s.metricsPath,
    nodeSelector: formatLabels(s.nodeSelector),
  };
}

export default function ServiceConfig({
  project,
  env,
  service,
  onSaved,
  joinArgs,
}: {
  project: string;
  env: string;
  service: Service;
  onSaved: () => void;
  joinArgs: (a: string[]) => string;
}) {
  const canEdit = useCan("developer");
  const [form, setForm] = useState(() => toForm(service, joinArgs));
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  // A página recarrega o serviço a cada 3s; só se repõe o formulário se
  // ninguém o estiver a editar.
  useEffect(() => {
    if (!dirty) setForm(toForm(service, joinArgs));
  }, [service, dirty]);

  function set<K extends keyof ReturnType<typeof toForm>>(key: K, value: string) {
    setForm((f) => ({ ...f, [key]: value }));
    setDirty(true);
  }

  async function save() {
    setError(null);
    setNotice(null);
    setSaving(true);
    const web = service.kind === "web";
    try {
      const nodeSelector = parseLabels(form.nodeSelector);
      await api(servicePath(project, env, service.slug), {
        method: "PATCH",
        body: JSON.stringify({
          ...(web
            ? { port: Number(form.port), healthPath: form.healthPath.trim(), healthTimeoutSeconds: Number(form.healthTimeoutSeconds) }
            : { healthTimeoutSeconds: Number(form.healthTimeoutSeconds) }),
          cmd: splitArgs(form.cmd),
          releaseCmd: splitArgs(form.releaseCmd),
          env: form.env
            .split("\n")
            .map((l) => l.trim())
            .filter(Boolean),
          vcpu: Number(form.vcpu),
          memoryMb: Number(form.memoryMb),
          scalingMode: form.scalingMode,
          replicas: Number(form.replicas),
          minReplicas: Number(form.minReplicas),
          maxReplicas: Number(form.maxReplicas),
          cpuScaleUpPercent: Number(form.cpuScaleUpPercent),
          cpuScaleDownPercent: Number(form.cpuScaleDownPercent),
          metricsPort: Number(form.metricsPort) || 0,
          metricsPath: form.metricsPath.trim(),
          nodeSelector,
        }),
      });
      setDirty(false);
      setNotice("Gravado. O scaling já está a ser aplicado; o resto entra no próximo deploy (usa \"Redeploy\").");
      onSaved();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="card full">
      <h1>Configuração</h1>
      {error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}

      <h2>Execução (aplica-se no próximo deploy)</h2>
      <div className="form-grid">
        {service.kind === "web" && (
          <>
            <div>
              <label htmlFor="cfg-port">Porta</label>
              <input id="cfg-port" value={form.port} onChange={(e) => set("port", e.target.value)} />
            </div>
            <div>
              <label htmlFor="cfg-health">Health check (vazio = só TCP)</label>
              <input id="cfg-health" value={form.healthPath} onChange={(e) => set("healthPath", e.target.value)} />
            </div>
          </>
        )}
        <div>
          <label htmlFor="cfg-timeout">Tempo para ficar saudável (s)</label>
          <input id="cfg-timeout" value={form.healthTimeoutSeconds} onChange={(e) => set("healthTimeoutSeconds", e.target.value)} />
        </div>
        <div>
          <label htmlFor="cfg-vcpu">vCPU por réplica</label>
          <input id="cfg-vcpu" value={form.vcpu} onChange={(e) => set("vcpu", e.target.value)} />
        </div>
        <div>
          <label htmlFor="cfg-mem">Memória por réplica (MB)</label>
          <input id="cfg-mem" value={form.memoryMb} onChange={(e) => set("memoryMb", e.target.value)} />
        </div>
        <div>
          <label htmlFor="cfg-node-selector">Só em nós com as etiquetas (vazio = qualquer nó)</label>
          <input
            id="cfg-node-selector"
            value={form.nodeSelector}
            onChange={(e) => set("nodeSelector", e.target.value)}
            placeholder="gpu=nvidia, disco=ssd"
            title="As etiquetas dos nós vêem-se e mudam-se em Administração > Nós"
          />
        </div>
        <div>
          <label htmlFor="cfg-metrics-port">Porta de métricas (vazio = sem métricas)</label>
          <input id="cfg-metrics-port" value={form.metricsPort} onChange={(e) => set("metricsPort", e.target.value)} placeholder="9100" />
        </div>
        <div>
          <label htmlFor="cfg-metrics-path">Caminho de métricas</label>
          <input id="cfg-metrics-path" value={form.metricsPath} onChange={(e) => set("metricsPath", e.target.value)} placeholder="/metrics" />
        </div>
      </div>
      <p className="muted">
        Com porta de métricas, o Prometheus recolhe as métricas da app (formato Prometheus) e ficam no Grafana com as
        labels project, env e project_service.
      </p>
      <label htmlFor="cfg-cmd">Comando (vazio = o da imagem)</label>
      <input id="cfg-cmd" value={form.cmd} onChange={(e) => set("cmd", e.target.value)} placeholder="php artisan queue:work" />
      <label htmlFor="cfg-release">Release / migrations (corre antes de cada deploy; se falhar, o deploy pára)</label>
      <input
        id="cfg-release"
        value={form.releaseCmd}
        onChange={(e) => set("releaseCmd", e.target.value)}
        placeholder="php artisan migrate --force"
      />
      <label htmlFor="cfg-env">Variáveis de ambiente (NOME=valor, uma por linha; segredos com {"${secret:NOME}"})</label>
      <textarea id="cfg-env" rows={6} value={form.env} onChange={(e) => set("env", e.target.value)} />
      <p className="muted">
        As URLs dos addons do ambiente (ex: DATABASE_URL) e PORT são injetadas automaticamente; uma variável aqui com o
        mesmo nome sobrepõe-se.
      </p>

      <h2>Scaling (aplica-se já)</h2>
      <div className="form-grid">
        <div>
          <label htmlFor="cfg-mode">Modo</label>
          <select id="cfg-mode" value={form.scalingMode} onChange={(e) => set("scalingMode", e.target.value)}>
            <option value="fixed">fixo</option>
            <option value="auto">automático (CPU)</option>
          </select>
        </div>
        {form.scalingMode === "fixed" ? (
          <div>
            <label htmlFor="cfg-replicas">Réplicas</label>
            <input id="cfg-replicas" value={form.replicas} onChange={(e) => set("replicas", e.target.value)} />
          </div>
        ) : (
          <>
            <div>
              <label htmlFor="cfg-min">Mínimo</label>
              <input id="cfg-min" value={form.minReplicas} onChange={(e) => set("minReplicas", e.target.value)} />
            </div>
            <div>
              <label htmlFor="cfg-max">Máximo</label>
              <input id="cfg-max" value={form.maxReplicas} onChange={(e) => set("maxReplicas", e.target.value)} />
            </div>
            <div>
              <label htmlFor="cfg-up">Sobe acima de (% CPU)</label>
              <input id="cfg-up" value={form.cpuScaleUpPercent} onChange={(e) => set("cpuScaleUpPercent", e.target.value)} />
            </div>
            <div>
              <label htmlFor="cfg-down">Desce abaixo de (% CPU)</label>
              <input id="cfg-down" value={form.cpuScaleDownPercent} onChange={(e) => set("cpuScaleDownPercent", e.target.value)} />
            </div>
          </>
        )}
      </div>
      <div className="actions">
        <button className="secondary" disabled={saving || !dirty || !canEdit} onClick={save} title={canEdit ? "" : "o teu papel neste projeto só permite ver"}>
          {saving ? "A gravar..." : "Gravar configuração"}
        </button>
        {dirty && (
          <button
            className="secondary"
            onClick={() => {
              setDirty(false);
              setForm(toForm(service, joinArgs));
            }}
          >
            Descartar
          </button>
        )}
      </div>
    </div>
  );
}
