"use client";

import { useEffect, useState } from "react";
import type { AdminUser } from "@/lib/authClient";

const ROLES = ["user", "admin", "infra-admin", "super-admin"];

export default function AdminUsersClient() {
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState(ROLES[0]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [savingID, setSavingID] = useState<string | null>(null);

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
    setSavingID(id);
    try {
      await fetch(`/api/admin/users/${id}/role`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ role: newRole }),
      });
      await loadUsers();
    } finally {
      setSavingID(null);
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
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </>
  );
}
