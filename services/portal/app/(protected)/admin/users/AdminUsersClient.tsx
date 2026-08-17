"use client";

import { useEffect, useState } from "react";
import type { AdminUser, TokenPair } from "@/lib/authClient";

const ROLES = ["user", "admin", "infra-admin", "super-admin", "service"];

type MintedTokens = {
  user: AdminUser;
  pair: TokenPair;
};

export default function AdminUsersClient() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState(ROLES[0]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [savingID, setSavingID] = useState<string | null>(null);
  const [mintingID, setMintingID] = useState<string | null>(null);
  const [mintedTokens, setMintedTokens] = useState<MintedTokens | null>(null);

  async function loadUsers() {
    const res = await fetch("/api/admin/users");
    if (res.ok) setUsers(await res.json());
  }

  useEffect(() => {
    loadUsers();
  }, []);

  async function handleCreate(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const res = await fetch("/api/admin/users", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password, role }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data.message ?? "erro ao criar utilizador");
        return;
      }
      setEmail("");
      setPassword("");
      await loadUsers();
    } finally {
      setLoading(false);
    }
  }

  async function handleRoleChange(id: string, newRole: string) {
    setError(null);
    setMintedTokens(null);
    setSavingID(id);
    try {
      const res = await fetch(`/api/admin/users/${id}/role`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ role: newRole }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.message ?? "erro ao alterar role");
        return;
      }
      await loadUsers();
    } finally {
      setSavingID(null);
    }
  }

  async function handleMintTokens(user: AdminUser) {
    setError(null);
    setMintedTokens(null);
    setMintingID(user.id);
    try {
      const res = await fetch(`/api/admin/users/${encodeURIComponent(user.id)}/tokens`, { method: "POST" });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(data.message ?? "erro ao gerar tokens");
        return;
      }
      setMintedTokens({ user, pair: data });
    } finally {
      setMintingID(null);
    }
  }

  async function copyToClipboard(text: string) {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // O token continua visível para seleção manual.
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Criar utilizador</h1>
        <p className="muted">
          Ao contrário do registo público, esta conta já nasce com o e-mail
          verificado — és tu, como super-admin, que estás a garantir por ela.
        </p>
        {error && <div className="error">{error}</div>}
        <form onSubmit={handleCreate}>
          <label htmlFor="email">E-mail</label>
          <input id="email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
          <label htmlFor="password">Senha (mínimo 8 caracteres)</label>
          <input
            id="password"
            type="password"
            required
            minLength={8}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {role === "service" && (
            <p className="muted">
              A senha é exigida na criação, mas o autoscaler autentica-se com o refresh token gerado abaixo.
            </p>
          )}
          <label htmlFor="role">Role</label>
          <select id="role" value={role} onChange={(e) => setRole(e.target.value)}>
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          <button className="primary" type="submit" disabled={loading}>
            {loading ? "A criar..." : "Criar utilizador"}
          </button>
        </form>
      </div>

      <div className="card wide">
        <h1>Utilizadores</h1>
        {mintedTokens && (
          <div className="notice">
            <strong>Guarda estes tokens agora. Não serão mostrados novamente.</strong>
            <p>
              Conta: <strong>{mintedTokens.user.email}</strong>
            </p>
            <p>Refresh token para o campo de token ao criar o grupo:</p>
            <pre className="generated">{mintedTokens.pair.refresh_token}</pre>
            <button className="secondary" onClick={() => copyToClipboard(mintedTokens.pair.refresh_token)}>
              Copiar refresh token
            </button>
            <p>Access token:</p>
            <pre className="generated">{mintedTokens.pair.access_token}</pre>
            <div className="row">
              <button className="secondary" onClick={() => copyToClipboard(mintedTokens.pair.access_token)}>
                Copiar access token
              </button>
              <button className="secondary" onClick={() => setMintedTokens(null)}>
                Fechar
              </button>
            </div>
            <p className="muted">
              Ao criar o grupo, este refresh token é enviado como replicaAuthToken e torna-se
              LAUNCHER_REFRESH_TOKEN no autoscaler. Emitir outro par não revoga os anteriores.
            </p>
          </div>
        )}
        {users.length === 0 ? (
          <p className="muted">Nenhum utilizador ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>E-mail</th>
                <th>Role</th>
                <th>Criado</th>
                <th>E-mail verificado</th>
                <th>Ações</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td>{u.email}</td>
                  <td>
                    <select
                      value={u.role}
                      disabled={savingID === u.id}
                      onChange={(e) => handleRoleChange(u.id, e.target.value)}
                    >
                      {ROLES.map((r) => (
                        <option key={r} value={r}>
                          {r}
                        </option>
                      ))}
                    </select>
                  </td>
                  <td>{new Date(u.created_at).toLocaleDateString("pt-PT")}</td>
                  <td>{u.email_verified_at ? "sim" : "não"}</td>
                  <td>
                    {u.role === "service" && (
                      <button
                        className="secondary"
                        disabled={mintingID !== null || savingID !== null}
                        onClick={() => handleMintTokens(u)}
                      >
                        {mintingID === u.id ? "A gerar..." : "Gerar tokens"}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
