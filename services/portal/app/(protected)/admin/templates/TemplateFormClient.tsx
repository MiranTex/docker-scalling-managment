"use client";

import { copyText } from "@/lib/clipboard";
import Loading from "@/components/Skeleton";
import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import PageHeader from "@/components/PageHeader";
import type { ServiceTemplate } from "@/lib/templatesAdminClient";
import type { InstanceType } from "@/lib/templatesAdminClient";
import { formatInstanceType } from "@/lib/instanceTypes";
import type { SecretInfo } from "@/lib/secretsAdminClient";

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
  defaultInstanceType: "",
  labelsText: "",
  envRows: [] as EnvRow[],
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
    defaultInstanceType: t.defaultInstanceType ?? "",
    labelsText: labelsToText(t.labels),
    envRows: envToRows(t.env),
  };
}

// Monta o corpo enviado a PUT /v1/templates/{name} a partir do estado do
// formulário -- espelha store.Template do lado do templatesadmin (ver
// services/templatesadmin/internal/store/store.go). Só o que o container
// da aplicação precisa -- config de autoscaling não faz parte disto, ver
// "Criar grupo" em /admin/autoscaler.
function formToBody(form: typeof EMPTY_FORM) {
  return {
    image: form.image,
    cmd: linesToArray(form.cmdText),
    env: rowsToEnv(form.envRows),
    labels: parseLabels(form.labelsText),
    binds: linesToArray(form.bindsText),
    network: form.network.trim(),
    extraHosts: linesToArray(form.extraHostsText),
    defaultInstanceType: form.defaultInstanceType,
  };
}

// Pré-visualização do launch template tal como services/autoscaler/cmd/group
// o espera (ver executor.LaunchTemplate) -- omite arrays/mapas vazios,
// igual às tags `json:"...,omitempty"` do lado do Go. Só para conferir o
// resultado; não é preciso copiar isto para nenhum lado -- ver "Lançar
// containers sem autoscaling (launcher)" em demo/README.md.
function previewLaunchTemplateJSON(form: typeof EMPTY_FORM): string {
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

async function copyToClipboard(text: string) {
  try {
    await copyText(text);
  } catch {
    // Clipboard API indisponível (ex: contexto não seguro) -- o texto já
    // está visível no <pre>, o utilizador ainda consegue selecionar e
    // copiar à mão.
  }
}

export default function TemplateFormClient({ name }: { name?: string }) {
  const router = useRouter();
  const editingName = name ?? null;
  const [images, setImages] = useState<string[]>([]);
  const [secrets, setSecrets] = useState<SecretInfo[]>([]);
  const [instanceTypes, setInstanceTypes] = useState<InstanceType[]>([]);
  const [form, setForm] = useState(EMPTY_FORM);
  const [loaded, setLoaded] = useState(!name);
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [preview, setPreview] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      const [img, sec, it] = await Promise.all([
        fetch("/api/admin/templates/images"),
        fetch("/api/admin/secrets"),
        fetch("/api/admin/instance-types"),
      ]);
      if (img.ok) setImages((await img.json()) ?? []);
      if (sec.ok) setSecrets(await sec.json());
      if (it.ok) setInstanceTypes(((await it.json()) ?? []).filter((x: InstanceType) => x.enabled));
      if (name) {
        const res = await fetch("/api/admin/templates");
        if (res.ok) {
          const list: ServiceTemplate[] = await res.json();
          const found = list.find((t) => t.name === name);
          if (found) setForm(templateToForm(found));
          else setFormError("modelo não encontrado");
        } else {
          setFormError(`erro ${res.status} a carregar modelo`);
        }
        setLoaded(true);
      }
    })().catch(() => {
      setFormError("erro a carregar dados");
      setLoaded(true);
    });
  }, [name]);

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
      router.push("/admin/templates");
    } catch {
      setFormError("erro ao gravar modelo");
    } finally {
      setSaving(false);
    }
  }

  const imageOptions = useMemo(() => {
    const opts = [...images];
    if (form.image && !opts.includes(form.image)) opts.push(form.image);
    return opts;
  }, [images, form.image]);

  return (
    <>
      <PageHeader
        title={editingName ? `Editar "${editingName}"` : "Novo modelo"}
        description="Define a imagem, comando, variáveis, labels, volumes e rede do container."
        actions={
          <Link href="/admin/templates" className="secondary">
            Voltar
          </Link>
        }
      />
      <div className="split">
      <div className="card wide">
        {formError && <div className="error">{formError}</div>}
        {!loaded ? (
          <Loading />
        ) : (
          <>
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
          placeholder="auth-net"
        />

        <label htmlFor="tpl-instance-type">Tipo de instância por omissão</label>
        <select
          id="tpl-instance-type"
          value={form.defaultInstanceType}
          onChange={(e) => setForm((f) => ({ ...f, defaultInstanceType: e.target.value }))}
        >
          <option value="">(usar o default do launcher)</option>
          {instanceTypes.map((it) => (
            <option key={it.name} value={it.name}>
              {formatInstanceType(it)}
            </option>
          ))}
        </select>
        <p className="muted">
          Aplicado quando quem lança não escolhe nenhum. Gerir o catálogo em{" "}
          <a href="/admin/instance-types">/admin/instance-types</a>.
        </p>

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

        <div className="row" style={{ justifyContent: "flex-start" }}>
          <button className="primary" disabled={saving} onClick={handleSave}>
            {saving ? "A gravar..." : editingName ? "Gravar alterações" : "Criar modelo"}
          </button>
          <button className="secondary" onClick={() => setPreview(previewLaunchTemplateJSON(form))} disabled={!form.image.trim()}>
            Ver JSON
          </button>
          <Link href="/admin/templates" className="secondary">
            Cancelar
          </Link>
        </div>
          </>
        )}
      </div>

      {preview && (
        <div className="card wide">
          <div className="row">
            <h2 style={{ margin: 0 }}>Launch template (pré-visualização)</h2>
            <button className="secondary" onClick={() => copyToClipboard(preview)}>
              Copiar
            </button>
          </div>
          <pre className="generated">{preview}</pre>
        </div>
      )}
      </div>
    </>
  );
}
