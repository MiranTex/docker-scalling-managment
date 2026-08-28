"use client";

import { useEffect, useState } from "react";
import type { InstanceType } from "@/lib/templatesAdminClient";
import { formatMemory } from "@/lib/instanceTypes";
import ConfirmModal from "../database/ConfirmModal";

const EMPTY_FORM = {
  name: "",
  displayName: "",
  vcpu: "0.5",
  memoryMb: "512",
  pidsLimit: "0",
  enabled: true,
};

function typeToForm(it: InstanceType): typeof EMPTY_FORM {
  return {
    name: it.name,
    displayName: it.displayName,
    vcpu: String(it.vcpu),
    memoryMb: String(it.memoryMb),
    pidsLimit: String(it.pidsLimit),
    enabled: it.enabled,
  };
}

export default function InstanceTypesClient() {
  const [types, setTypes] = useState<InstanceType[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);

  const [form, setForm] = useState(EMPTY_FORM);
  const [editingName, setEditingName] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function loadTypes() {
    try {
      const res = await fetch("/api/admin/instance-types");
      if (!res.ok) {
        setListError(`erro ${res.status} a listar tipos de instância`);
        return;
      }
      setTypes(await res.json());
      setListError(null);
    } catch {
      setListError("erro a listar tipos de instância");
    }
  }

  useEffect(() => {
    loadTypes();
  }, []);

  function resetForm() {
    setForm(EMPTY_FORM);
    setEditingName(null);
    setFormError(null);
  }

  function startEdit(it: InstanceType) {
    setForm(typeToForm(it));
    setEditingName(it.name);
    setFormError(null);
  }

  const nameForSave = editingName ?? form.name.trim();

  async function handleSave() {
    setFormError(null);
    if (!nameForSave) {
      setFormError("nome é obrigatório");
      return;
    }
    const vcpu = Number(form.vcpu);
    const memoryMb = Number(form.memoryMb);
    if (!(vcpu > 0)) {
      setFormError("vCPU tem de ser maior que zero");
      return;
    }
    // Mesmo mínimo que o daemon Docker impõe -- validar aqui evita um
    // 400 que só apareceria depois do round-trip.
    if (!(memoryMb >= 6)) {
      setFormError("memória tem de ser pelo menos 6 MB");
      return;
    }
    setSaving(true);
    try {
      const res = await fetch(`/api/admin/instance-types/${encodeURIComponent(nameForSave)}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          displayName: form.displayName.trim(),
          vcpu,
          memoryMb,
          pidsLimit: Number(form.pidsLimit) || 0,
          enabled: form.enabled,
        }),
      });
      const data = await res.json();
      if (!res.ok) {
        setFormError(data.message ?? "erro ao gravar tipo de instância");
        return;
      }
      await loadTypes();
      setEditingName(nameForSave);
      setForm((f) => ({ ...f, name: nameForSave }));
    } catch {
      setFormError("erro ao gravar tipo de instância");
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
      const res = await fetch(`/api/admin/instance-types/${encodeURIComponent(target)}`, { method: "DELETE" });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setDeleteError(data.message ?? "erro ao apagar tipo de instância");
        return;
      }
      if (editingName === target) resetForm();
      await loadTypes();
    } catch {
      setDeleteError("erro ao apagar tipo de instância");
    }
  }

  return (
    <>
      <div className="card wide">
        <h1>Tipos de instância</h1>
        <p className="muted">
          Catálogo de tamanhos aplicados como limites reais do Docker (CPU e memória) a cada
          container lançado -- no espírito dos instance types da AWS. Escolhe-se o tipo ao lançar
          em <a href="/admin/launcher">/admin/launcher</a>; um modelo pode definir um tipo por
          omissão em <a href="/admin/templates">/admin/templates</a>.
        </p>
        {listError && <div className="error">{listError}</div>}
        {deleteError && <div className="error">{deleteError}</div>}

        {!types ? (
          <p className="muted">A carregar...</p>
        ) : types.length === 0 ? (
          <p className="muted">Nenhum tipo definido.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nome</th>
                <th>vCPU</th>
                <th>Memória</th>
                <th>Estado</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {types.map((it) => (
                <tr key={it.name}>
                  <td>
                    <code>{it.name}</code>
                    {it.displayName && <div className="muted">{it.displayName}</div>}
                  </td>
                  <td>{it.vcpu}</td>
                  <td>{formatMemory(it.memoryMb)}</td>
                  <td>{it.enabled ? "ativo" : "desativado"}</td>
                  <td>
                    <button className="secondary" onClick={() => startEdit(it)}>
                      Editar
                    </button>{" "}
                    <button className="danger" onClick={() => setDeleteTarget(it.name)}>
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
        <h1>{editingName ? `Editar "${editingName}"` : "Criar tipo"}</h1>
        {formError && <div className="error">{formError}</div>}

        <label htmlFor="it-name">Nome</label>
        <input
          id="it-name"
          type="text"
          value={form.name}
          disabled={!!editingName}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
          placeholder="t1.small"
        />

        <label htmlFor="it-display-name">Descrição</label>
        <input
          id="it-display-name"
          type="text"
          value={form.displayName}
          onChange={(e) => setForm((f) => ({ ...f, displayName: e.target.value }))}
          placeholder="Small (0.5 vCPU / 512 MB)"
        />

        <label htmlFor="it-vcpu">vCPU</label>
        <input
          id="it-vcpu"
          type="number"
          step="0.05"
          min="0.05"
          value={form.vcpu}
          onChange={(e) => setForm((f) => ({ ...f, vcpu: e.target.value }))}
        />

        <label htmlFor="it-memory">Memória (MB)</label>
        <input
          id="it-memory"
          type="number"
          step="1"
          min="6"
          value={form.memoryMb}
          onChange={(e) => setForm((f) => ({ ...f, memoryMb: e.target.value }))}
        />

        <label htmlFor="it-pids">Limite de processos (0 = ilimitado)</label>
        <input
          id="it-pids"
          type="number"
          step="1"
          min="0"
          value={form.pidsLimit}
          onChange={(e) => setForm((f) => ({ ...f, pidsLimit: e.target.value }))}
        />

        <label>
          <input
            type="checkbox"
            checked={form.enabled}
            onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
          />{" "}
          Ativo
        </label>
        <p className="muted">
          Desativar retira o tipo de circulação sem afetar containers que já correm com ele.
        </p>

        <button onClick={handleSave} disabled={saving}>
          {saving ? "A gravar..." : editingName ? "Guardar" : "Criar"}
        </button>{" "}
        {editingName && (
          <button className="secondary" onClick={resetForm}>
            Cancelar
          </button>
        )}
      </div>

      {deleteTarget && (
        <ConfirmModal
          title={`Apagar "${deleteTarget}"`}
          message="Containers já em execução mantêm os limites que receberam; só deixa de ser possível lançar novos com este tipo."
          expectedText={deleteTarget}
          confirmLabel="Apagar"
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </>
  );
}
