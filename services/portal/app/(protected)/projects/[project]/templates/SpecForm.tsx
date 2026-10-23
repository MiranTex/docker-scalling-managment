"use client";

import { joinArgs, splitArgs, type TemplateSpec } from "@/lib/platformClient";

// Formulário da configuração de um modelo -- os mesmos campos da
// configuração de um serviço. kindLocked: numa versão nova o tipo não muda.
export type SpecFormState = {
  image: string;
  notes: string;
  kind: "web" | "worker";
  port: string;
  healthPath: string;
  healthTimeoutSeconds: string;
  cmd: string;
  releaseCmd: string;
  env: string;
  vcpu: string;
  memoryMb: string;
  scalingMode: "fixed" | "auto";
  replicas: string;
  minReplicas: string;
  maxReplicas: string;
  cpuScaleUpPercent: string;
  cpuScaleDownPercent: string;
  metricsPort: string;
  metricsPath: string;
};

export function emptySpecForm(): SpecFormState {
  return {
    image: "",
    notes: "",
    kind: "web",
    port: "8080",
    healthPath: "/",
    healthTimeoutSeconds: "120",
    cmd: "",
    releaseCmd: "",
    env: "",
    vcpu: "0.5",
    memoryMb: "512",
    scalingMode: "fixed",
    replicas: "1",
    minReplicas: "1",
    maxReplicas: "3",
    cpuScaleUpPercent: "70",
    cpuScaleDownPercent: "20",
    metricsPort: "",
    metricsPath: "",
  };
}

export function specFormFrom(spec: TemplateSpec, image: string): SpecFormState {
  return {
    image,
    notes: "",
    kind: spec.kind,
    port: String(spec.port || ""),
    healthPath: spec.healthPath,
    healthTimeoutSeconds: String(spec.healthTimeoutSeconds),
    cmd: joinArgs(spec.cmd),
    releaseCmd: joinArgs(spec.releaseCmd),
    env: spec.env.join("\n"),
    vcpu: String(spec.vcpu),
    memoryMb: String(spec.memoryMb),
    scalingMode: spec.scalingMode,
    replicas: String(spec.replicas),
    minReplicas: String(spec.minReplicas),
    maxReplicas: String(spec.maxReplicas),
    cpuScaleUpPercent: String(spec.cpuScaleUpPercent),
    cpuScaleDownPercent: String(spec.cpuScaleDownPercent),
    metricsPort: String(spec.metricsPort || ""),
    metricsPath: spec.metricsPath,
  };
}

export function specFromForm(f: SpecFormState): TemplateSpec {
  const web = f.kind === "web";
  return {
    kind: f.kind,
    port: web ? Number(f.port) || 0 : 0,
    healthPath: web ? f.healthPath.trim() : "",
    healthTimeoutSeconds: Number(f.healthTimeoutSeconds) || 0,
    cmd: splitArgs(f.cmd),
    releaseCmd: splitArgs(f.releaseCmd),
    env: f.env
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean),
    vcpu: Number(f.vcpu) || 0,
    memoryMb: Number(f.memoryMb) || 0,
    scalingMode: f.scalingMode,
    replicas: Number(f.replicas) || 0,
    minReplicas: Number(f.minReplicas) || 0,
    maxReplicas: Number(f.maxReplicas) || 0,
    cpuScaleUpPercent: Number(f.cpuScaleUpPercent) || 0,
    cpuScaleDownPercent: Number(f.cpuScaleDownPercent) || 0,
    metricsPort: Number(f.metricsPort) || 0,
    metricsPath: f.metricsPath.trim(),
  };
}

export default function SpecForm({
  form,
  onChange,
  kindLocked,
}: {
  form: SpecFormState;
  onChange: (f: SpecFormState) => void;
  kindLocked?: boolean;
}) {
  const set = <K extends keyof SpecFormState>(k: K, v: SpecFormState[K]) => onChange({ ...form, [k]: v });
  const web = form.kind === "web";
  return (
    <>
      <div className="form-grid">
        <div>
          <label htmlFor="tpl-image">Imagem do primeiro deploy (com tag)</label>
          <input id="tpl-image" value={form.image} onChange={(e) => set("image", e.target.value)} placeholder="ghcr.io/eu/app:1.0.0" />
        </div>
        <div>
          <label htmlFor="tpl-kind">Tipo</label>
          <select
            id="tpl-kind"
            value={form.kind}
            disabled={kindLocked}
            onChange={(e) => set("kind", e.target.value as SpecFormState["kind"])}
          >
            <option value="web">web</option>
            <option value="worker">worker</option>
          </select>
        </div>
        {web && (
          <>
            <div>
              <label htmlFor="tpl-port">Porta</label>
              <input id="tpl-port" value={form.port} onChange={(e) => set("port", e.target.value)} />
            </div>
            <div>
              <label htmlFor="tpl-health">Health check (vazio = só TCP)</label>
              <input id="tpl-health" value={form.healthPath} onChange={(e) => set("healthPath", e.target.value)} />
            </div>
          </>
        )}
        <div>
          <label htmlFor="tpl-timeout">Tempo para ficar saudável (s)</label>
          <input id="tpl-timeout" value={form.healthTimeoutSeconds} onChange={(e) => set("healthTimeoutSeconds", e.target.value)} />
        </div>
        <div>
          <label htmlFor="tpl-vcpu">vCPU por réplica</label>
          <input id="tpl-vcpu" value={form.vcpu} onChange={(e) => set("vcpu", e.target.value)} />
        </div>
        <div>
          <label htmlFor="tpl-mem">Memória por réplica (MB)</label>
          <input id="tpl-mem" value={form.memoryMb} onChange={(e) => set("memoryMb", e.target.value)} />
        </div>
        <div>
          <label htmlFor="tpl-mport">Porta de métricas (opcional)</label>
          <input id="tpl-mport" value={form.metricsPort} onChange={(e) => set("metricsPort", e.target.value)} />
        </div>
        <div>
          <label htmlFor="tpl-mpath">Caminho de métricas</label>
          <input id="tpl-mpath" value={form.metricsPath} onChange={(e) => set("metricsPath", e.target.value)} placeholder="/metrics" />
        </div>
      </div>
      <label htmlFor="tpl-cmd">Comando (vazio = o da imagem)</label>
      <input id="tpl-cmd" value={form.cmd} onChange={(e) => set("cmd", e.target.value)} />
      <label htmlFor="tpl-release">Release / migrations</label>
      <input id="tpl-release" value={form.releaseCmd} onChange={(e) => set("releaseCmd", e.target.value)} />
      <label htmlFor="tpl-env">Variáveis de ambiente (NOME=valor por linha; segredos com {"${secret:NOME}"})</label>
      <textarea id="tpl-env" rows={4} value={form.env} onChange={(e) => set("env", e.target.value)} />
      <div className="form-grid">
        <div>
          <label htmlFor="tpl-mode">Scaling</label>
          <select id="tpl-mode" value={form.scalingMode} onChange={(e) => set("scalingMode", e.target.value as SpecFormState["scalingMode"])}>
            <option value="fixed">fixo</option>
            <option value="auto">automático (CPU)</option>
          </select>
        </div>
        {form.scalingMode === "fixed" ? (
          <div>
            <label htmlFor="tpl-replicas">Réplicas</label>
            <input id="tpl-replicas" value={form.replicas} onChange={(e) => set("replicas", e.target.value)} />
          </div>
        ) : (
          <>
            <div>
              <label htmlFor="tpl-min">Mínimo</label>
              <input id="tpl-min" value={form.minReplicas} onChange={(e) => set("minReplicas", e.target.value)} />
            </div>
            <div>
              <label htmlFor="tpl-max">Máximo</label>
              <input id="tpl-max" value={form.maxReplicas} onChange={(e) => set("maxReplicas", e.target.value)} />
            </div>
            <div>
              <label htmlFor="tpl-up">Sobe acima de (% CPU)</label>
              <input id="tpl-up" value={form.cpuScaleUpPercent} onChange={(e) => set("cpuScaleUpPercent", e.target.value)} />
            </div>
            <div>
              <label htmlFor="tpl-down">Desce abaixo de (% CPU)</label>
              <input id="tpl-down" value={form.cpuScaleDownPercent} onChange={(e) => set("cpuScaleDownPercent", e.target.value)} />
            </div>
          </>
        )}
      </div>
      <label htmlFor="tpl-notes">Nota desta versão (o que muda)</label>
      <input id="tpl-notes" value={form.notes} onChange={(e) => set("notes", e.target.value)} placeholder="ex: sobe memória para 1 GB" />
    </>
  );
}
