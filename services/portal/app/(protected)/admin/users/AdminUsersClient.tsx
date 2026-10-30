"use client";

import { copyText } from "@/lib/clipboard";
import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import PageHeader from "@/components/PageHeader";
import { authError } from "@/lib/errors";
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
  const [creating, setCreating] = useState(false);
  // Definir a password de uma conta (ex: quem a esqueceu).
  const [pwUser, setPwUser] = useState<AdminUser | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [pwError, setPwError] = useState<string | null>(null);
  const [pwDone, setPwDone] = useState(false);
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
        setError(authError(data, "erro ao criar utilizador"));
        return;
      }
      setEmail("");
      setPassword("");
      setCreating(false);
      await loadUsers();
    } finally {
      setLoading(false);
    }
  }

  async function handleSetPassword(e: React.FormEvent) {
    e.preventDefault();
    if (!pwUser) return;
    setPwError(null);
    const res = await fetch(`/api/admin/users/${encodeURIComponent(pwUser.id)}/password`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: newPassword }),
    });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      setPwError(authError(data, "não foi possível definir a password"));
      return;
    }
    setNewPassword("");
    setPwDone(true);
  }

  // Desligar o 2FA de uma conta (ex: perdeu o telemóvel e os códigos de
  // recuperação); volta a ativá-lo no próximo login, se for admin.
  async function handleDisableMFA(user: AdminUser) {
    if (!confirm(`Desligar o 2FA de ${user.email}? As sessões abertas desta conta são terminadas.`)) return;
    setError(null);
    const res = await fetch(`/api/admin/users/${encodeURIComponent(user.id)}/mfa`, { method: "DELETE" });
    if (!res.ok) {
      const data = await res.json().catch(() => ({}));
      setError(authError(data, "não foi possível desligar o 2FA"));
      return;
    }
    await loadUsers();
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
        setError(authError(data, "erro ao alterar role"));
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
        setError(authError(data, "erro ao gerar tokens"));
        return;
      }
      setMintedTokens({ user, pair: data });
    } finally {
      setMintingID(null);
    }
  }

  async function copyToClipboard(text: string) {
    try {
      await copyText(text);
    } catch {
      // O token continua visível para seleção manual.
    }
  }

  return (
    <>
      <PageHeader
        title="Utilizadores"
        actions={
          <button
            className="primary"
            onClick={() => {
              setError(null);
              setCreating(true);
            }}
          >
            Novo utilizador
          </button>
        }
      />
      {error && !creating && <div className="error">{error}</div>}
      {creating && (
        <Modal title="Criar utilizador" onClose={() => setCreating(false)}>
          <p className="muted">
            Ao contrário do registo público, esta conta já nasce com o e-mail verificado — és tu, como super-admin, que
            estás a garantir por ela.
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
                A senha é exigida na criação, mas o autoscaler autentica-se com o refresh token gerado na lista.
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
            <div className="row">
              <button className="primary" type="submit" disabled={loading}>
                {loading ? "A criar..." : "Criar utilizador"}
              </button>
              <button className="secondary" type="button" onClick={() => setCreating(false)}>
                Cancelar
              </button>
            </div>
          </form>
        </Modal>
      )}

      <div className="card wide">
        <h2>Contas</h2>
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
                <th>2FA</th>
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
                  <td>{u.mfa_enabled ? <span className="badge active">ativo</span> : <span className="muted">—</span>}</td>
                  <td className="row" style={{ gap: 6, flexWrap: "nowrap" }}>
                    {u.role !== "service" && (
                      <button
                        className="secondary"
                        onClick={() => {
                          setPwUser(u);
                          setNewPassword("");
                          setPwError(null);
                          setPwDone(false);
                        }}
                      >
                        Mudar password
                      </button>
                    )}
                    {u.mfa_enabled && (
                      <button className="secondary" onClick={() => handleDisableMFA(u)}>
                        Desligar 2FA
                      </button>
                    )}
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

      {pwUser && (
        <Modal title={`Password de ${pwUser.email}`} onClose={() => setPwUser(null)}>
          {pwDone ? (
            <>
              <div className="notice">Password definida. As sessões abertas desta conta foram terminadas.</div>
              <p className="muted">Passa-lhe a password por um canal seguro; pode trocá-la depois em "A minha conta".</p>
              <button className="secondary" onClick={() => setPwUser(null)}>
                Fechar
              </button>
            </>
          ) : (
            <form onSubmit={handleSetPassword}>
              {pwError && <div className="error">{pwError}</div>}
              <label htmlFor="new-password">Password nova (mínimo 8 caracteres)</label>
              <input
                id="new-password"
                type="text"
                required
                minLength={8}
                autoComplete="off"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
              />
              <button className="primary" type="submit">
                Definir password
              </button>
            </form>
          )}
        </Modal>
      )}
    </>
  );
}
