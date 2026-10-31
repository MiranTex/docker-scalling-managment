"use client";

import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { api, projectPath, type RegistryCredential } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";

// Credenciais de registries privados, usadas no pull das imagens de deploy
// de qualquer ambiente do projeto.
export default function RegistriesCard({ project }: { project: string }) {
  const canEdit = useCan("developer");
  const [list, setList] = useState<RegistryCredential[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [form, setForm] = useState({ registry: "", username: "", password: "" });
  const [adding, setAdding] = useState(false);

  async function load() {
    try {
      setList(await api<RegistryCredential[]>(projectPath(project, "registries")));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project]);

  async function save() {
    setError(null);
    try {
      await api(projectPath(project, "registries", form.registry.trim()), {
        method: "PUT",
        body: JSON.stringify({ username: form.username.trim(), password: form.password }),
      });
      setForm({ registry: "", username: "", password: "" });
      setAdding(false);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function remove(registry: string) {
    if (!confirm(`Apagar as credenciais de ${registry}?`)) return;
    try {
      await api(projectPath(project, "registries", registry), { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <div className="card wide">
      <div className="page-header">
        <h2>Registries privados</h2>
        {canEdit && (
          <button className="primary" onClick={() => setAdding(true)}>
            Acrescentar registry
          </button>
        )}
      </div>
      <p className="muted">
        Usadas no pull das imagens de deploy (ex: ghcr.io com um token de leitura). Sem credenciais, o pull é anónimo.
      </p>
      {!adding && error && <div className="error">{error}</div>}
      {list && list.length > 0 && (
        <table>
          <tbody>
            {list.map((r) => (
              <tr key={r.registry}>
                <td>
                  <code>{r.registry}</code>
                </td>
                <td>{r.username}</td>
                <td>{new Date(r.updatedAt).toLocaleString("pt-PT")}</td>
                <td>
                  {canEdit && (
                  <button className="danger" onClick={() => remove(r.registry)}>
                    Apagar
                  </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && (
        <Modal title="Credenciais de um registry" onClose={() => setAdding(false)}>
          {error && <div className="error">{error}</div>}
          <p className="muted">Gravar para um registry que já está na lista substitui as credenciais dele.</p>
          <div className="form-grid">
        <div>
          <label htmlFor="reg-host">Registry</label>
          <input id="reg-host" value={form.registry} onChange={(e) => setForm({ ...form, registry: e.target.value })} placeholder="ghcr.io" />
        </div>
        <div>
          <label htmlFor="reg-user">Utilizador</label>
          <input id="reg-user" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />
        </div>
        <div>
          <label htmlFor="reg-pass">Password / token</label>
          <input
            id="reg-pass"
            type="password"
            autoComplete="off"
            value={form.password}
            onChange={(e) => setForm({ ...form, password: e.target.value })}
          />
        </div>
      </div>
          <button
            className="primary"
            disabled={!form.registry.trim() || !form.username.trim() || !form.password}
            onClick={save}
          >
            Gravar credenciais
          </button>
        </Modal>
      )}
    </div>
  );
}
