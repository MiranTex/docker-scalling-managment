"use client";

import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import type { ApiKeyCreated, ApiKeyListItem } from "@/lib/authClient";

const TTL_OPTIONS = [
  { label: "Sem expiração", value: "" },
  { label: "30 dias", value: String(30 * 24 * 60 * 60) },
  { label: "90 dias", value: String(90 * 24 * 60 * 60) },
  { label: "1 ano", value: String(365 * 24 * 60 * 60) },
];

export default function TokensPage() {
  const [keys, setKeys] = useState<ApiKeyListItem[]>([]);
  const [scopes, setScopes] = useState("");
  // Acrescenta o scope "platform": dá a este token acesso à API de
  // projetos (só funciona se a tua conta for infra-admin ou super-admin).
  const [platform, setPlatform] = useState(false);
  const [ttl, setTtl] = useState("");
  const [created, setCreated] = useState<ApiKeyCreated | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);

  async function loadKeys() {
    const res = await fetch("/api/tokens");
    if (res.ok) setKeys(await res.json());
  }

  useEffect(() => {
    loadKeys();
  }, []);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const scopeList = scopes
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      if (platform && !scopeList.includes("platform")) scopeList.push("platform");
      const res = await fetch("/api/tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          scopes: scopeList,
          ttl_seconds: ttl ? Number(ttl) : undefined,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data.message ?? "erro ao criar token");
        return;
      }
      setCreated(data);
      setScopes("");
      setPlatform(false);
      setCreating(false);
      await loadKeys();
    } finally {
      setLoading(false);
    }
  }

  async function handleRevoke(id: string) {
    if (!confirm("Revogar este token? A ação não pode ser desfeita.")) return;
    await fetch(`/api/tokens/${id}`, { method: "DELETE" });
    await loadKeys();
  }

  return (
    <div className="card wide">
      <div className="page-header">
        <h1>Os meus tokens</h1>
        <button className="primary" onClick={() => setCreating(true)}>
          Novo token
        </button>
      </div>
      {created && (
        <div className="notice">
          Token criado — copia agora, não será mostrado outra vez:
          <code className="key">{created.key}</code>
          <button className="secondary" onClick={() => setCreated(null)}>
            Fechar
          </button>
        </div>
      )}
      {keys.length === 0 ? (
        <p className="muted">Ainda não tens nenhum token.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>ID</th>
              <th>Scopes</th>
              <th>Criado</th>
              <th>Expira</th>
              <th>Estado</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.id}>
                <td>{k.id.slice(0, 8)}…</td>
                <td>{k.scopes?.join(", ") || "—"}</td>
                <td>{new Date(k.created_at).toLocaleDateString("pt-PT")}</td>
                <td>{k.expires_at ? new Date(k.expires_at).toLocaleDateString("pt-PT") : "nunca"}</td>
                <td>
                  <span className={`badge ${k.status}`}>{k.status}</span>
                </td>
                <td>
                  {k.status === "active" && (
                    <button className="danger" onClick={() => handleRevoke(k.id)}>
                      Revogar
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {creating && (
        <Modal title="Novo token" onClose={() => setCreating(false)}>
          {error && <div className="error">{error}</div>}
          <form onSubmit={handleCreate}>
            <label htmlFor="scopes">Scopes (separados por vírgula, opcional)</label>
            <input id="scopes" placeholder="read:x, write:x" value={scopes} onChange={(e) => setScopes(e.target.value)} />
            <label className="checkbox">
              <input type="checkbox" checked={platform} onChange={(e) => setPlatform(e.target.checked)} /> Acesso à API da
              plataforma (projetos, deploys, segredos...)
            </label>
            {platform && (
              <p className="error">
                Este token passa a poder fazer tudo o que fazes nos projetos, a partir de qualquer script e até fora desta
                máquina. Usa uma validade e guarda-o como um segredo. Só funciona enquanto a tua conta for infra-admin ou
                super-admin.
              </p>
            )}
            <label htmlFor="ttl">Validade</label>
            <select id="ttl" value={ttl} onChange={(e) => setTtl(e.target.value)}>
              {TTL_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <button className="primary" type="submit" disabled={loading}>
              {loading ? "A criar..." : "Criar token"}
            </button>
          </form>
        </Modal>
      )}
    </div>
  );
}
