"use client";

import { useEffect, useMemo, useState } from "react";
import type { ServiceTemplate } from "@/lib/templatesAdminClient";
import type { SecretInfo } from "@/lib/secretsAdminClient";
import ConfirmModal from "../database/ConfirmModal";

type EnvRow = {
  key: string;
  kind: "static" | "secret";
  value: string;
};

const EMPTY_FORM = {
  name: "",
  image: "",
  imageFreeText: false,
  cmdText: "",
  bindsText: "",
  extraHostsText: "",
  network: "",
  labelsText: "",
  envRows: [] as EnvRow[],
  targetService: "",
  backendPort: "80",
  minReplicas: "1",
  maxReplicas: "3",
  cpuScaleUpPercent: "50",
  cpuScaleDownPercent: "20",
  hostProxyPort: "",
  hostMetricsPort: "",
};

const SECRET_REF_RE = /^\$\{secret:(.+)\}$/;

function linesToArray(text: string): string[] {
  return text
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
}

function parseLabels(text: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const line of linesToArray(text)) {
    const eq = line.indexOf("=");
    if (eq === -1) continue;
    out[line.slice(0, eq).trim()] = line.slice(eq + 1).trim();
  }
  return out;
}

function labelsToText(labels: Record<string, string>): string {
  return Object.entries(labels)
    .map(([k, v]) => `${k}=${v}`)
    .join("\n");
}

function envToRows(env: string[]): EnvRow[] {
  return env.map((entry) => {
    const eq = entry.indexOf("=");
    const key = eq === -1 ? entry : entry.slice(0, eq);
    const rawValue = eq === -1 ? "" : entry.slice(eq + 1);
    const secretMatch = rawValue.match(SECRET_REF_RE);
    if (secretMatch) {
      return { key, kind: "secret" as const, value: secretMatch[1] };
    }
    return { key, kind: "static" as const, value: rawValue };
  });
}

function rowsToEnv(rows: EnvRow[]): string[] {
  return rows
    .filter((r) => r.key.trim())
    .map((r) => `${r.key.trim()}=${r.kind === "secret" ? `\${secret:${r.value}}` : r.value}`);
}

function templateToForm(t: ServiceTemplate): typeof EMPTY_FORM {
  return {
    name: t.name,
    image: t.image,
    imageFreeText: false,
    cmdText: t.cmd.join("\n"),
    bindsText: t.binds.join("\n"),
    extraHostsText: t.extraHosts.join("\n"),
    network: t.network,
    labelsText: labelsToText(t.labels),
    envRows: envToRows(t.env),
    targetService: t.targetService,
    backendPort: String(t.backendPort),
    minReplicas: String(t.minReplicas),
    maxReplicas: String(t.maxReplicas),
    cpuScaleUpPercent: String(t.cpuScaleUpPercent),
    cpuScaleDownPercent: String(t.cpuScaleDownPercent),
    hostProxyPort: t.hostProxyPort != null ? String(t.hostProxyPort) : "",
    hostMetricsPort: t.hostMetricsPort != null ? String(t.hostMetricsPort) : "",
  };
}

// Monta o corpo enviado a PUT /v1/templates/{name} a partir do estado do
// formulário -- espelha store.Template do lado do templatesadmin (ver
// services/templatesadmin/internal/store/store.go).
function formToBody(form: typeof EMPTY_FORM) {
  return {
    image: form.image,
    cmd: linesToArray(form.cmdText),
    env: rowsToEnv(form.envRows),
    labels: parseLabels(form.labelsText),
    binds: linesToArray(form.bindsText),
    network: form.network.trim(),
    extraHosts: linesToArray(form.extraHostsText),
    targetService: form.targetService.trim(),
    backendPort: Number(form.backendPort) || 80,
    minReplicas: Number(form.minReplicas) || 0,
    maxReplicas: Number(form.maxReplicas) || 0,
    cpuScaleUpPercent: Number(form.cpuScaleUpPercent) || 0,
    cpuScaleDownPercent: Number(form.cpuScaleDownPercent) || 0,
    hostProxyPort: form.hostProxyPort.trim() ? Number(form.hostProxyPort) : undefined,
    hostMetricsPort: form.hostMetricsPort.trim() ? Number(form.hostMetricsPort) : undefined,
  };
}

// Gera o launch-template.json tal como services/autoscaler/cmd/group o
// espera (ver executor.LaunchTemplate) -- omite arrays/mapas vazios,
// igual às tags `json:"...,omitempty"` do lado do Go.
function generateLaunchTemplateJSON(form: typeof EMPTY_FORM): string {
  const body = formToBody(form);
  const out: Record<string, unknown> = { image: body.image };
  if (body.cmd.length) out.cmd = body.cmd;
  if (body.env.length) out.env = body.env;
  if (Object.keys(body.labels).length) out.labels = body.labels;
  if (body.binds.length) out.binds = body.binds;
  if (body.network) out.network = body.network;
  if (body.extraHosts.length) out.extraHosts = body.extraHosts;
  return JSON.stringify(out, null, 2);
}

// Gera o bloco docker-compose do group correspondente -- mesmo formato
// do "group-demo" comentado em demo/docker-compose.yml.
function generateComposeBlock(form: typeof EMPTY_FORM): string {
  const name = form.name.trim() || form.targetService.trim() || "<nome>";
  const templateFile = `launch-template.${name}.json`;
  const usesSecrets = form.envRows.some((r) => r.kind === "secret");

  const lines: string[] = [];
  lines.push(`  group-${name}:`);
  lines.push(`    image: autoscaler-group:latest`);
  lines.push(`    environment:`);
  lines.push(`      TARGET_SERVICE: ${form.targetService.trim() || name}`);
  lines.push(`      BACKEND_PORT: "${form.backendPort || "80"}"`);
  lines.push(`      MIN_REPLICAS: "${form.minReplicas || "1"}"`);
  lines.push(`      MAX_REPLICAS: "${form.maxReplicas || "3"}"`);
  lines.push(`      CPU_SCALE_UP_PERCENT: "${form.cpuScaleUpPercent || "50"}"`);
  lines.push(`      CPU_SCALE_DOWN_PERCENT: "${form.cpuScaleDownPercent || "20"}"`);
  lines.push(`      RECONCILE_TICK_SECONDS: "3"`);
  lines.push(`      LOG_FORMAT: "json"`);
  lines.push(`      LOG_LEVEL: "info"`);
  lines.push(`      METRICS_ADDR: ":9090"`);
  lines.push(`      LAUNCH_TEMPLATE_FILE: "/etc/autoscaler/${templateFile}"`);
  if (usesSecrets) {
    lines.push(`      # Este modelo referencia \${secret:NOME} -- preencha também`);
    lines.push(`      # AUTH_SERVICE_URL/AUTH_ISSUER/AUTH_AUDIENCE, SECRETSADMIN_SERVICE_URL`);
    lines.push(`      # e SECRETS_REFRESH_TOKEN (ver demo/README.md, secção de segredos).`);
  }
  lines.push(`    labels:`);
  lines.push(`      prometheus.scrape: "true"`);
  lines.push(`      prometheus.port: "9090"`);
  if (form.hostProxyPort.trim() || form.hostMetricsPort.trim()) {
    lines.push(`    ports:`);
    if (form.hostProxyPort.trim()) lines.push(`      - "${form.hostProxyPort.trim()}:8090"`);
    if (form.hostMetricsPort.trim()) lines.push(`      - "${form.hostMetricsPort.trim()}:9090"`);
  }
  lines.push(`    volumes:`);
  lines.push(`      - /var/run/docker.sock:/var/run/docker.sock`);
  lines.push(`      - ./${templateFile}:/etc/autoscaler/${templateFile}:ro`);
  lines.push(`    networks:`);
  lines.push(`      - observability-net`);
  return lines.join("\n");
}

async function copyToClipboard(text: string) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    // Clipboard API indisponível (ex: contexto não seguro) -- o texto já
    // está visível no <pre>, o utilizador ainda consegue selecionar e
    // copiar à mão.
  }
}

export default function TemplatesAdminClient() {
  const [templates, setTemplates] = useState<ServiceTemplate[] | null>(null);
  const [images, setImages] = useState<string[]>([]);
  const [secrets, setSecrets] = useState<SecretInfo[]>([]);
  const [listError, setListError] = useState<string | null>(null);

  const [form, setForm] = useState(EMPTY_FORM);
  const [editingName, setEditingName] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [generated, setGenerated] = useState<{ json: string; compose: string } | null>(null);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function loadTemplates() {
    try {
      const res = await fetch("/api/admin/templates");
      if (!res.ok) {
        setListError(`erro ${res.status} a listar modelos`);
        return;
      }
      setTemplates(await res.json());
      setListError(null);
    } catch {
      setListError("erro a listar modelos");
    }
  }

  async function loadImages() {
    const res = await fetch("/api/admin/templates/images");
    if (res.ok) setImages((await res.json()) ?? []);
  }

  async function loadSecrets() {
    const res = await fetch("/api/admin/secrets");
    if (res.ok) setSecrets(await res.json());
  }

  useEffect(() => {
    loadTemplates();
    loadImages();
    loadSecrets();
  }, []);

  function resetForm() {
    setForm(EMPTY_FORM);
    setEditingName(null);
    setGenerated(null);
    setFormError(null);
  }

  function startEdit(t: ServiceTemplate) {
    setForm(templateToForm(t));
    setEditingName(t.name);
    setGenerated(null);
    setFormError(null);
  }

  function updateEnvRow(index: number, patch: Partial<EnvRow>) {
    setForm((f) => ({
      ...f,
      envRows: f.envRows.map((r, i) => (i === index ? { ...r, ...patch } : r)),
    }));
  }

  function addEnvRow() {
    setForm((f) => ({ ...f, envRows: [...f.envRows, { key: "", kind: "static", value: "" }] }));
  }

  function removeEnvRow(index: number) {
    setForm((f) => ({ ...f, envRows: f.envRows.filter((_, i) => i !== index) }));
  }

  const nameForSave = editingName ?? form.name.trim();

  async function handleSave() {
    setFormError(null);
    if (!nameForSave) {
      setFormError("nome é obrigatório");
      return;
    }
    if (!form.image.trim()) {
      setFormError("imagem é obrigatória");
      return;
    }
    if (!form.targetService.trim()) {
      setFormError("nome do serviço (targetService) é obrigatório");
      return;
    }
    setSaving(true);
    try {
      const res = await fetch(`/api/admin/templates/${encodeURIComponent(nameForSave)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(formToBody(form)),
      });
      const data = await res.json();
      if (!res.ok) {
        setFormError(data.message ?? "erro ao gravar modelo");
        return;
      }
      await loadTemplates();
      setEditingName(nameForSave);
      setForm((f) => ({ ...f, name: nameForSave }));
    } catch {
      setFormError("erro ao gravar modelo");
    } finally {
      setSaving(false);
    }
  }

  function handleGenerate() {
    setGenerated({
      json: generateLaunchTemplateJSON(form),
      compose: generateComposeBlock({ ...form, name: nameForSave }),
    });
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleteTarget(null);
    setDeleteError(null);
    try {
      const res = await fetch(`/api/admin/templates/${encodeURIComponent(target)}`, { method: "DELETE" });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setDeleteError(data.message ?? "erro ao apagar modelo");
        return;
      }
      if (editingName === target) resetForm();
      await loadTemplates();
    } catch {
      setDeleteError("erro ao apagar modelo");
    }
  }

  const imageOptions = useMemo(() => {
    const opts = [...images];
    if (form.image && !opts.includes(form.image)) opts.push(form.image);
    return opts;
  }, [images, form.image]);

  return (
    <>
      <div className="card wide">
        <h1>Modelos de serviço</h1>
        <p className="muted">
          Cada modelo guarda o launch template (imagem, env, binds, rede -- ver{" "}
          <code>services/autoscaler/internal/executor.LaunchTemplate</code>) e a configuração do group
          correspondente (réplicas, portas). Isto é um <strong>gerador</strong>: gravar aqui não sobe
          nenhum container -- use o botão &quot;Gerar&quot; para obter o JSON e o bloco docker-compose e
          aplique-os você mesmo (ver <code>demo/README.md</code>, &quot;Criar um novo serviço
          autoscalado&quot;).
        </p>
        {listError && <div className="error">{listError}</div>}
        {deleteError && <div className="error">{deleteError}</div>}

        {!templates ? (
          <p className="muted">A carregar...</p>
        ) : templates.length === 0 ? (
          <p className="muted">Nenhum modelo ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nome</th>
                <th>Imagem</th>
                <th>Serviço</th>
                <th>Réplicas</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {templates.map((t) => (
                <tr key={t.name}>
                  <td>
                    <code>{t.name}</code>
                  </td>
                  <td>{t.image}</td>
                  <td>{t.targetService}</td>
                  <td>
                    {t.minReplicas}-{t.maxReplicas}
                  </td>
                  <td>
                    <button className="secondary" onClick={() => startEdit(t)}>
                      Editar
                    </button>{" "}
                    <button className="danger" onClick={() => setDeleteTarget(t.name)}>
                      Apagar
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card wide">
        <h1>{editingName ? `Editar "${editingName}"` : "Criar modelo"}</h1>
        {formError && <div className="error">{formError}</div>}

        <label htmlFor="tpl-name">Nome do modelo</label>
        <input
          id="tpl-name"
          type="text"
          value={form.name}
          disabled={!!editingName}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
          placeholder="laravel-app"
        />

        <label htmlFor="tpl-image">Imagem (já buildada/pulled no Docker do host)</label>
        {!form.imageFreeText ? (
          <select
            id="tpl-image"
            value={form.image}
            onChange={(e) =>
              e.target.value === "__free__"
                ? setForm((f) => ({ ...f, imageFreeText: true, image: "" }))
                : setForm((f) => ({ ...f, image: e.target.value }))
            }
          >
            <option value="">-- escolha --</option>
            {imageOptions.map((img) => (
              <option key={img} value={img}>
                {img}
              </option>
            ))}
            <option value="__free__">Outra (digitar)...</option>
          </select>
        ) : (
          <input
            type="text"
            value={form.image}
            onChange={(e) => setForm((f) => ({ ...f, image: e.target.value }))}
            placeholder="sail-8.4/app"
          />
        )}

        <label htmlFor="tpl-target-service">Nome do serviço (TARGET_SERVICE)</label>
        <input
          id="tpl-target-service"
          type="text"
          value={form.targetService}
          onChange={(e) => setForm((f) => ({ ...f, targetService: e.target.value }))}
          placeholder="laravel-app"
        />

        <label>Variáveis de ambiente</label>
        {form.envRows.map((row, i) => (
          <div className="row" key={i} style={{ gap: "0.5rem", marginBottom: "0.5rem" }}>
            <input
              type="text"
              value={row.key}
              onChange={(e) => updateEnvRow(i, { key: e.target.value })}
              placeholder="CHAVE"
              style={{ marginBottom: 0 }}
            />
            <select
              value={row.kind}
              onChange={(e) => updateEnvRow(i, { kind: e.target.value as EnvRow["kind"], value: "" })}
              style={{ marginBottom: 0, maxWidth: "9rem" }}
            >
              <option value="static">Estático</option>
              <option value="secret">Secrets manager</option>
            </select>
            {row.kind === "static" ? (
              <input
                type="text"
                value={row.value}
                onChange={(e) => updateEnvRow(i, { value: e.target.value })}
                placeholder="valor"
                style={{ marginBottom: 0 }}
              />
            ) : (
              <select
                value={row.value}
                onChange={(e) => updateEnvRow(i, { value: e.target.value })}
                style={{ marginBottom: 0 }}
              >
                <option value="">-- escolha um segredo --</option>
                {secrets.map((s) => (
                  <option key={s.name} value={s.name}>
                    {s.name}
                  </option>
                ))}
              </select>
            )}
            <button className="danger" onClick={() => removeEnvRow(i)}>
              Remover
            </button>
          </div>
        ))}
        <button className="secondary" onClick={addEnvRow}>
          + Variável
        </button>
        {secrets.length === 0 && (
          <p className="muted">
            Nenhum segredo criado ainda -- crie um em <code>/admin/secrets</code> para poder referenciá-lo
            aqui.
          </p>
        )}

        <label htmlFor="tpl-cmd" style={{ marginTop: "1rem" }}>
          Comando (opcional, uma entrada por linha)
        </label>
        <textarea
          id="tpl-cmd"
          rows={2}
          value={form.cmdText}
          onChange={(e) => setForm((f) => ({ ...f, cmdText: e.target.value }))}
        />

        <label htmlFor="tpl-binds">Binds (uma entrada por linha, formato host:container)</label>
        <textarea
          id="tpl-binds"
          rows={3}
          value={form.bindsText}
          onChange={(e) => setForm((f) => ({ ...f, bindsText: e.target.value }))}
          placeholder={"/home/dev/projeto:/var/www/html"}
        />

        <label htmlFor="tpl-network">Rede Docker</label>
        <input
          id="tpl-network"
          type="text"
          value={form.network}
          onChange={(e) => setForm((f) => ({ ...f, network: e.target.value }))}
          placeholder="observability-net"
        />

        <label htmlFor="tpl-extra-hosts">Extra hosts (uma entrada por linha, formato host:ip)</label>
        <textarea
          id="tpl-extra-hosts"
          rows={2}
          value={form.extraHostsText}
          onChange={(e) => setForm((f) => ({ ...f, extraHostsText: e.target.value }))}
          placeholder={"host.docker.internal:host-gateway"}
        />

        <label htmlFor="tpl-labels">Labels (uma entrada por linha, formato chave=valor)</label>
        <textarea
          id="tpl-labels"
          rows={2}
          value={form.labelsText}
          onChange={(e) => setForm((f) => ({ ...f, labelsText: e.target.value }))}
        />

        <div className="row" style={{ gap: "1rem" }}>
          <div style={{ flex: 1 }}>
            <label htmlFor="tpl-backend-port">Porta do backend</label>
            <input
              id="tpl-backend-port"
              type="number"
              value={form.backendPort}
              onChange={(e) => setForm((f) => ({ ...f, backendPort: e.target.value }))}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label htmlFor="tpl-min">Réplicas mín.</label>
            <input
              id="tpl-min"
              type="number"
              value={form.minReplicas}
              onChange={(e) => setForm((f) => ({ ...f, minReplicas: e.target.value }))}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label htmlFor="tpl-max">Réplicas máx.</label>
            <input
              id="tpl-max"
              type="number"
              value={form.maxReplicas}
              onChange={(e) => setForm((f) => ({ ...f, maxReplicas: e.target.value }))}
            />
          </div>
        </div>

        <div className="row" style={{ gap: "1rem" }}>
          <div style={{ flex: 1 }}>
            <label htmlFor="tpl-cpu-up">Escalar acima de (% CPU)</label>
            <input
              id="tpl-cpu-up"
              type="number"
              value={form.cpuScaleUpPercent}
              onChange={(e) => setForm((f) => ({ ...f, cpuScaleUpPercent: e.target.value }))}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label htmlFor="tpl-cpu-down">Escalar abaixo de (% CPU)</label>
            <input
              id="tpl-cpu-down"
              type="number"
              value={form.cpuScaleDownPercent}
              onChange={(e) => setForm((f) => ({ ...f, cpuScaleDownPercent: e.target.value }))}
            />
          </div>
        </div>

        <div className="row" style={{ gap: "1rem" }}>
          <div style={{ flex: 1 }}>
            <label htmlFor="tpl-host-proxy-port">Porta de host (proxy, opcional)</label>
            <input
              id="tpl-host-proxy-port"
              type="number"
              value={form.hostProxyPort}
              onChange={(e) => setForm((f) => ({ ...f, hostProxyPort: e.target.value }))}
            />
          </div>
          <div style={{ flex: 1 }}>
            <label htmlFor="tpl-host-metrics-port">Porta de host (métricas, opcional)</label>
            <input
              id="tpl-host-metrics-port"
              type="number"
              value={form.hostMetricsPort}
              onChange={(e) => setForm((f) => ({ ...f, hostMetricsPort: e.target.value }))}
            />
          </div>
        </div>

        <div className="row" style={{ gap: "0.5rem" }}>
          <button className="secondary" disabled={saving} onClick={handleSave}>
            {saving ? "A gravar..." : editingName ? "Gravar alterações" : "Criar modelo"}
          </button>
          <button className="secondary" onClick={handleGenerate} disabled={!form.image.trim()}>
            Gerar
          </button>
          {editingName && (
            <button className="secondary" onClick={resetForm}>
              Novo modelo
            </button>
          )}
        </div>
      </div>

      {generated && (
        <div className="card wide">
          <h1>Ficheiros gerados</h1>

          <div className="row">
            <label style={{ marginBottom: 0 }}>
              launch-template.{nameForSave || "<nome>"}.json
            </label>
            <button className="secondary" onClick={() => copyToClipboard(generated.json)}>
              Copiar
            </button>
          </div>
          <pre className="generated">{generated.json}</pre>

          <div className="row" style={{ marginTop: "1rem" }}>
            <label style={{ marginBottom: 0 }}>Bloco docker-compose (demo/docker-compose.yml)</label>
            <button className="secondary" onClick={() => copyToClipboard(generated.compose)}>
              Copiar
            </button>
          </div>
          <pre className="generated">{generated.compose}</pre>
        </div>
      )}

      {deleteTarget && (
        <ConfirmModal
          title="Confirmar remoção"
          message={`Isto apaga o modelo "${deleteTarget}" -- não afeta nenhum group já a correr a partir de um launch template já gerado/copiado a partir dele.`}
          expectedText={deleteTarget}
          confirmLabel="Apagar"
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </>
  );
}
