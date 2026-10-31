"use client";

import { useEffect, useState } from "react";
import { api, envPath, type Addon, type Bucket } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";
import DomainsCard from "./services/[svc]/DomainsCard";

const EMPTY = { name: "", public: false, cors: "" };

function parseOrigins(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((o) => o.trim())
    .filter(Boolean);
}

// StorageCard: o acesso de fora a um addon storage -- o endereço público
// e os buckets, cada um privado ou público (CDN), com CORS e domínios.
export default function StorageCard({ project, env, addon, onChanged }: { project: string; env: string; addon: Addon; onChanged: () => void }) {
  const canEdit = useCan("developer");
  const [buckets, setBuckets] = useState<Bucket[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [form, setForm] = useState(EMPTY);
  const [cors, setCors] = useState<Record<string, string>>({});
  const [domainsOf, setDomainsOf] = useState<string | null>(null);
  const base = envPath(project, env, "addons", addon.slug);

  async function load() {
    try {
      const list = await api<Bucket[]>(`${base}/buckets`);
      setBuckets(list);
      setCors(Object.fromEntries(list.map((b) => [b.name, b.corsOrigins.join(", ")])));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [base, addon.publicEndpoint]);

  async function run(fn: () => Promise<unknown>, done?: string) {
    setError(null);
    setNotice(null);
    try {
      await fn();
      if (done) setNotice(done);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const togglePublicEndpoint = () =>
    run(async () => {
      await api(base, { method: "PATCH", body: JSON.stringify({ publicEndpoint: !addon.publicEndpoint }) });
      onChanged();
    }, addon.publicEndpoint
      ? `Endereço público desligado. Os serviços perdem ${addon.envVar}_PUBLIC_URL no próximo deploy.`
      : `Endereço público ligado. Os serviços recebem ${addon.envVar}_PUBLIC_URL no próximo deploy.`);

  const setPublic = (b: Bucket, value: boolean) => {
    if (value && !confirm(`Tornar ${b.name} público? Qualquer pessoa com o endereço de um ficheiro passa a poder lê-lo (listar o bucket continua fechado).`)) return;
    return run(() => api(`${base}/buckets/${encodeURIComponent(b.name)}`, { method: "PATCH", body: JSON.stringify({ public: value }) }));
  };

  const saveCors = (b: Bucket) =>
    run(
      () => api(`${base}/buckets/${encodeURIComponent(b.name)}`, { method: "PATCH", body: JSON.stringify({ corsOrigins: parseOrigins(cors[b.name] ?? "") }) }),
      `CORS de ${b.name} guardado.`,
    );

  const remove = (b: Bucket) => {
    if (!confirm(`Apagar o bucket ${b.name}? Só é possível se estiver vazio.`)) return;
    return run(() => api(`${base}/buckets/${encodeURIComponent(b.name)}`, { method: "DELETE" }), `Bucket ${b.name} apagado.`);
  };

  const create = () =>
    run(async () => {
      await api(`${base}/buckets`, {
        method: "POST",
        body: JSON.stringify({ name: form.name.trim(), public: form.public, corsOrigins: parseOrigins(form.cors) }),
      });
      setForm(EMPTY);
    }, `Bucket ${form.name.trim()} criado.`);

  return (
    <div>
      {error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}

      <h2>Endereço público</h2>
      <p className="muted">
        Sem ele, o storage só é acessível aos serviços deste ambiente. Ligado, fica em HTTPS no endereço abaixo: os
        buckets privados abrem-se com links assinados pela app; os públicos, por qualquer pessoa. Escrever exige sempre
        as credenciais.
      </p>
      <label className="checkbox">
        <input type="checkbox" checked={!!addon.publicEndpoint} disabled={!canEdit} onChange={togglePublicEndpoint} /> endereço
        público
      </label>
      {addon.publicUrl && (
        <p>
          <code>{addon.publicUrl}</code> -- nos serviços como <code>{addon.envVar}_PUBLIC_URL</code>
        </p>
      )}

      <h2>Buckets</h2>
      {!buckets ? (
        <p className="muted">A carregar...</p>
      ) : (
        // Em ecrãs estreitos a tabela desliza na horizontal em vez de sair
        // do modal.
        <div style={{ overflowX: "auto" }}>
        <table>
          <thead>
            <tr>
              <th>Bucket</th>
              <th>Leitura</th>
              <th>CORS (origens do browser)</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {buckets.map((b) => (
              <tr key={b.name}>
                <td>
                  <strong>{b.name}</strong>
                  {b.name === "app" && <div className="muted">o de {addon.envVar}_BUCKET</div>}
                  {b.url && (
                    <div className="muted">
                      <code>{b.url}/…</code>
                    </div>
                  )}
                  {b.domains.length > 0 && (
                    <div className="muted">
                      {b.domains.map((d) => (
                        <div key={d.hostname}>
                          <code>{d.hostname}</code> {d.status === "active" ? "" : "(à espera do DNS)"}
                        </div>
                      ))}
                    </div>
                  )}
                </td>
                <td>
                  <select value={b.public ? "public" : "private"} disabled={!canEdit} onChange={(e) => setPublic(b, e.target.value === "public")}>
                    <option value="private">privada (links assinados)</option>
                    <option value="public">pública (CDN)</option>
                  </select>
                </td>
                <td>
                  <div className="row">
                    <input
                      style={{ minWidth: 220 }}
                      value={cors[b.name] ?? ""}
                      disabled={!canEdit}
                      onChange={(e) => setCors({ ...cors, [b.name]: e.target.value })}
                      placeholder="https://app.loja.pt"
                    />
                    {canEdit && (cors[b.name] ?? "") !== b.corsOrigins.join(", ") && (
                      <button className="secondary" onClick={() => saveCors(b)}>
                        Guardar
                      </button>
                    )}
                  </div>
                </td>
                <td>
                  <div className="actions">
                    {b.public && (
                      <button className="secondary" onClick={() => setDomainsOf(domainsOf === b.name ? null : b.name)}>
                        Domínios
                      </button>
                    )}
                    {canEdit && b.name !== "app" && (
                      <button className="danger" onClick={() => remove(b)}>
                        Apagar
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        </div>
      )}

      {domainsOf && buckets?.find((b) => b.name === domainsOf)?.public && (
        <DomainsCard
          base={`${base}/buckets/${encodeURIComponent(domainsOf)}/domains`}
          title={`Domínios do bucket ${domainsOf}`}
          owner={`o bucket ${domainsOf}`}
          placeholder="cdn.loja.pt"
        />
      )}

      {canEdit && (
        <>
          <h2>Novo bucket</h2>
          <div className="form-grid">
            <div>
              <label htmlFor="bucket-name">Nome</label>
              <input id="bucket-name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="media" />
            </div>
            <div>
              <label htmlFor="bucket-cors">CORS (opcional)</label>
              <input id="bucket-cors" value={form.cors} onChange={(e) => setForm({ ...form, cors: e.target.value })} placeholder="https://app.loja.pt" />
            </div>
          </div>
          <label className="checkbox">
            <input type="checkbox" checked={form.public} onChange={(e) => setForm({ ...form, public: e.target.checked })} /> leitura
            pública (CDN)
          </label>
          <button className="primary" disabled={!form.name.trim()} onClick={create}>
            Criar bucket
          </button>
        </>
      )}
    </div>
  );
}
