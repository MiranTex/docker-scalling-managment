"use client";

import { useEffect, useState } from "react";
import { formatMemory } from "@/lib/instanceTypes";
import { api, projectPath, type ProjectDetail, type Usage } from "@/lib/platformClient";

// Nome e quotas do projeto, com o que está alocado agora ao lado.
export default function SettingsCard({ detail, onSaved }: { detail: ProjectDetail; onSaved: () => void }) {
  const [form, setForm] = useState({
    name: detail.name,
    quotaVcpu: String(detail.quotaVcpu || ""),
    quotaMemoryMb: String(detail.quotaMemoryMb || ""),
  });
  const [usage, setUsage] = useState<Usage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    const load = () =>
      api<Usage>(projectPath(detail.slug, "usage"))
        .then(setUsage)
        .catch(() => setUsage(null));
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [detail.slug]);

  async function save() {
    setError(null);
    setNotice(null);
    try {
      await api(projectPath(detail.slug), {
        method: "PATCH",
        body: JSON.stringify({
          name: form.name.trim(),
          quotaVcpu: Number(form.quotaVcpu) || 0,
          quotaMemoryMb: Number(form.quotaMemoryMb) || 0,
        }),
      });
      setNotice("Gravado.");
      onSaved();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const pct = (used: number, quota: number) => (quota > 0 ? ` (${Math.round((used / quota) * 100)}%)` : "");

  return (
    <div className="card wide">
      <h2>Definições e quotas</h2>
      {error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}
      {usage && (
        <p>
          Alocado agora: <strong>{usage.vcpu.toFixed(2)} vCPU</strong>
          {pct(usage.vcpu, detail.quotaVcpu)} · <strong>{formatMemory(usage.memoryMb)}</strong>
          {pct(usage.memoryMb, detail.quotaMemoryMb)} · {usage.containers} container(s)
        </p>
      )}
      <div className="form-grid">
        <div>
          <label htmlFor="proj-name">Nome</label>
          <input id="proj-name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
        </div>
        <div>
          <label htmlFor="proj-vcpu">Quota de vCPU (vazio = sem limite)</label>
          <input id="proj-vcpu" value={form.quotaVcpu} onChange={(e) => setForm({ ...form, quotaVcpu: e.target.value })} />
        </div>
        <div>
          <label htmlFor="proj-mem">Quota de memória MB (vazio = sem limite)</label>
          <input
            id="proj-mem"
            value={form.quotaMemoryMb}
            onChange={(e) => setForm({ ...form, quotaMemoryMb: e.target.value })}
          />
        </div>
      </div>
      <p className="muted">
        Conta tudo o que corre no projeto (réplicas e addons). Durante um deploy as réplicas novas e antigas coexistem
        por instantes -- deixa margem para isso.
      </p>
      <button className="secondary" onClick={save}>
        Gravar
      </button>
    </div>
  );
}
