"use client";

import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { api, envPath, type EnvSecret } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";

export default function SecretsTab({ project, env }: { project: string; env: string }) {
  const canEdit = useCan("developer");
  const [secrets, setSecrets] = useState<EnvSecret[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [saving, setSaving] = useState(false);
  const [open, setOpen] = useState(false);

  async function load() {
    try {
      setSecrets(await api<EnvSecret[]>(envPath(project, env, "secrets")));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project, env]);

  async function save() {
    setError(null);
    setSaving(true);
    try {
      await api(envPath(project, env, "secrets", name.trim()), { method: "PUT", body: JSON.stringify({ value }) });
      setName("");
      setValue("");
      setOpen(false);
      await load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function remove(secretName: string) {
    if (!confirm(`Apagar o segredo ${secretName}?`)) return;
    try {
      await api(envPath(project, env, "secrets", secretName), { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <>
      <div className="page-header">
      <p className="muted" style={{ margin: 0 }}>
        Usa num serviço como <code>{"NOME=${secret:NOME}"}</code>. Os valores nunca voltam a ser mostrados. Os
        segredos geridos pela plataforma (credenciais de addons) não se editam à mão.
      </p>
        {canEdit && (
        <button className="primary" onClick={() => setOpen(true)}>
          Criar / substituir
        </button>
        )}
      </div>
      {!open && error && <div className="error">{error}</div>}
      {!secrets ? (
        <p className="muted">A carregar...</p>
      ) : secrets.length === 0 ? (
        <p className="muted">Nenhum segredo ainda.</p>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Nome</th>
              <th>Gerido por</th>
              <th>Atualizado</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {secrets.map((s) => (
              <tr key={s.name}>
                <td>
                  <code>{s.name}</code>
                </td>
                <td>{s.managedBy ? <span className="badge">{s.managedBy}</span> : "—"}</td>
                <td>
                  {new Date(s.updatedAt).toLocaleString("pt-PT")} · {s.updatedBy}
                </td>
                <td>
                  {!s.managedBy && canEdit && (
                    <button className="danger" onClick={() => remove(s.name)}>
                      Apagar
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {open && (
        <Modal title="Criar / substituir segredo" onClose={() => setOpen(false)}>
          {error && <div className="error">{error}</div>}
      <div className="form-grid">
        <div>
          <label htmlFor="secret-name">Nome</label>
          <input id="secret-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="STRIPE_KEY" />
        </div>
        <div>
          <label htmlFor="secret-value">Valor</label>
          <input
            id="secret-value"
            type="password"
            autoComplete="off"
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
        </div>
      </div>
      <button className="primary" disabled={saving || !name.trim()} onClick={save}>
        {saving ? "A gravar..." : "Gravar"}
      </button>
          <button className="secondary" onClick={() => setOpen(false)}>
            Cancelar
          </button>
        </Modal>
      )}
    </>
  );
}
