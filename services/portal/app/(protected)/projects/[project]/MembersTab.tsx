"use client";

import { useEffect, useState } from "react";
import { api, projectPath, type Member } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";

const ROLES: { value: Member["role"]; label: string; hint: string }[] = [
  { value: "viewer", label: "viewer", hint: "vê tudo, não muda nada (nem corre comandos)" },
  { value: "developer", label: "developer", hint: "deploys, configuração, segredos, addons, jobs, domínios" },
  { value: "admin", label: "admin", hint: "o mesmo, mais membros e ambientes" },
];

export default function MembersTab({ project }: { project: string }) {
  const canManage = useCan("admin");
  const [members, setMembers] = useState<Member[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Member["role"]>("developer");
  const [saving, setSaving] = useState(false);

  async function load() {
    try {
      setMembers(await api<Member[]>(projectPath(project, "members")));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project]);

  async function add() {
    setError(null);
    setSaving(true);
    try {
      await api(projectPath(project, "members"), { method: "POST", body: JSON.stringify({ email: email.trim(), role }) });
      setEmail("");
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function changeRole(m: Member, next: Member["role"]) {
    try {
      await api(projectPath(project, "members", m.userId), { method: "PATCH", body: JSON.stringify({ role: next }) });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function remove(m: Member) {
    if (!confirm(`Tirar ${m.email} do projeto? Deixa de o ver de imediato.`)) return;
    try {
      await api(projectPath(project, "members", m.userId), { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <div className="card wide">
      <p className="muted">
        Quem pode ver ou mexer neste projeto. Os administradores da plataforma (infra-admin) veem e gerem todos os
        projetos sem precisarem de estar aqui.
      </p>
      {error && <div className="error">{error}</div>}
      {!members ? (
        <p className="muted">A carregar...</p>
      ) : members.length === 0 ? (
        <p className="muted">Ainda sem membros.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Conta</th>
              <th>Papel</th>
              <th>Desde</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.userId}>
                <td>{m.email}</td>
                <td>
                  {canManage ? (
                    <select value={m.role} onChange={(e) => changeRole(m, e.target.value as Member["role"])}>
                      {ROLES.map((r) => (
                        <option key={r.value} value={r.value}>
                          {r.label}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <span className="badge">{m.role}</span>
                  )}
                </td>
                <td>{new Date(m.addedAt).toLocaleString("pt-PT")}</td>
                <td>
                  {canManage && (
                    <button className="danger" onClick={() => remove(m)}>
                      Remover
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {canManage && (
        <>
          <h2>Adicionar membro</h2>
          <p className="muted">A pessoa tem de já ter conta (registo no portal).</p>
          <div className="form-grid">
            <div>
              <label htmlFor="member-email">Email</label>
              <input id="member-email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="ana@exemplo.pt" />
            </div>
            <div>
              <label htmlFor="member-role">Papel</label>
              <select id="member-role" value={role} onChange={(e) => setRole(e.target.value as Member["role"])}>
                {ROLES.map((r) => (
                  <option key={r.value} value={r.value}>
                    {r.label} -- {r.hint}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <button className="primary" disabled={saving || !email.trim()} onClick={add}>
            {saving ? "A adicionar..." : "Adicionar"}
          </button>
        </>
      )}
    </div>
  );
}
