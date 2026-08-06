"use client";

import { useEffect, useState } from "react";
import type { SecretInfo } from "@/lib/secretsAdminClient";
import ConfirmModal from "../database/ConfirmModal";

export default function SecretsAdminClient() {
  const [secrets, setSecrets] = useState<SecretInfo[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);

  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function loadSecrets() {
    try {
      const res = await fetch("/api/admin/secrets");
      if (!res.ok) {
        setListError(`erro ${res.status} a listar segredos`);
        return;
      }
      setSecrets(await res.json());
      setListError(null);
    } catch {
      setListError("erro a listar segredos");
    }
  }

  useEffect(() => {
    loadSecrets();
  }, []);

  async function handleSave() {
    setFormError(null);
    if (!name.trim()) {
      setFormError("nome é obrigatório");
      return;
    }
    if (!value) {
      setFormError("valor é obrigatório");
      return;
    }
    setSaving(true);
    try {
      const res = await fetch(`/api/admin/secrets/${encodeURIComponent(name)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ value }),
      });
      const data = await res.json();
      if (!res.ok) {
        setFormError(data.message ?? "erro ao gravar segredo");
        return;
      }
      setName("");
      setValue("");
      await loadSecrets();
    } catch {
      setFormError("erro ao gravar segredo");
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleteTarget(null);
    setDeleteError(null);
    try {
      const res = await fetch(`/api/admin/secrets/${encodeURIComponent(target)}`, { method: "DELETE" });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setDeleteError(data.message ?? "erro ao apagar segredo");
        return;
      }
      await loadSecrets();
    } catch {
      setDeleteError("erro ao apagar segredo");
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Segredos</h1>
        <p className="muted">
          Valores referenciados nos launch templates do autoscaler como{" "}
          <code>{"${secret:NOME}"}</code> (ver services/autoscaler/internal/secretsclient). Cifrados
          em repouso -- uma vez gravado, um valor nunca volta a ser mostrado aqui, só pode ser
          substituído.
        </p>
        {listError && <div className="error">{listError}</div>}
        {deleteError && <div className="error">{deleteError}</div>}

        {!secrets ? (
          <p className="muted">A carregar...</p>
        ) : secrets.length === 0 ? (
          <p className="muted">Nenhum segredo ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nome</th>
                <th>Atualizado</th>
                <th>Por</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {secrets.map((s) => (
                <tr key={s.name}>
                  <td>
                    <code>{s.name}</code>
                  </td>
                  <td>{new Date(s.updated_at).toLocaleString("pt-PT")}</td>
                  <td>{s.updated_by}</td>
                  <td>
                    <button className="danger" onClick={() => setDeleteTarget(s.name)}>
                      Apagar
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card wide">
        <h1>Criar / substituir</h1>
        <p className="muted">
          Substituir o valor de um nome já usado num launch template aplica-se à PRÓXIMA réplica
          criada por esse group (não às já existentes) -- ver services/autoscaler/cmd/group/main.go,
          applyScaleUp.
        </p>
        {formError && <div className="error">{formError}</div>}

        <label htmlFor="secret-name">Nome</label>
        <input
          id="secret-name"
          type="text"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="auth-db-password"
        />
        <label htmlFor="secret-value">Valor</label>
        <input
          id="secret-value"
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          autoComplete="off"
        />
        <div className="row">
          <button className="secondary" disabled={saving} onClick={handleSave}>
            {saving ? "A gravar..." : "Gravar"}
          </button>
        </div>
      </div>

      {deleteTarget && (
        <ConfirmModal
          title="Confirmar remoção"
          message={`Isto apaga o segredo "${deleteTarget}" -- qualquer launch template que ainda o referencie via \${secret:${deleteTarget}} passa a falhar no próximo scale up.`}
          expectedText={deleteTarget}
          confirmLabel="Apagar"
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </>
  );
}
