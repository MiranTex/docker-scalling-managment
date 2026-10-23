"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import type { ServiceTemplate } from "@/lib/templatesAdminClient";
import PageHeader from "@/components/PageHeader";
import ConfirmModal from "../database/ConfirmModal";

export default function TemplatesAdminClient() {
  const [templates, setTemplates] = useState<ServiceTemplate[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function loadTemplates() {
    try {
      const res = await fetch("/api/admin/templates");
      if (!res.ok) {
        setListError(`erro ${res.status} a listar modelos`);
        return;
      }
      setTemplates(await res.json());
      setListError(null);
    } catch {
      setListError("erro a listar modelos");
    }
  }

  useEffect(() => {
    loadTemplates();
  }, []);

  async function handleDelete() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleteTarget(null);
    setDeleteError(null);
    try {
      const res = await fetch(`/api/admin/templates/${encodeURIComponent(target)}`, { method: "DELETE" });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setDeleteError(data.message ?? "erro ao apagar modelo");
        return;
      }
      await loadTemplates();
    } catch {
      setDeleteError("erro ao apagar modelo");
    }
  }

  return (
    <>
      <PageHeader
        title="Modelos de serviço"
        actions={
          <Link href="/admin/templates/new" className="primary">
            Novo modelo
          </Link>
        }
      />
      <div className="card wide">
        <p className="muted">
          Só o que o container da aplicação precisa -- imagem, comando, variáveis de ambiente,
          labels, volumes, rede (ver <code>services/autoscaler/internal/executor.LaunchTemplate</code>).
          Não inclui réplicas nem thresholds de CPU: essa config é decidida ao lançar um group a
          partir de um modelo, em <a href="/admin/autoscaler">/admin/autoscaler</a> ("Criar grupo").
          Para lançar uma instância "solo" (sem autoscaling nenhum), ver{" "}
          <a href="/admin/launcher">/admin/launcher</a>.
        </p>
        {listError && <div className="error">{listError}</div>}
        {deleteError && <div className="error">{deleteError}</div>}

        {!templates ? (
          <p className="muted">A carregar...</p>
        ) : templates.length === 0 ? (
          <p className="muted">Nenhum modelo ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nome</th>
                <th>Imagem</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {templates.map((t) => (
                <tr key={t.name}>
                  <td>
                    <code>{t.name}</code>
                  </td>
                  <td>{t.image}</td>
                  <td>
                    <Link href={`/admin/templates/${encodeURIComponent(t.name)}`} className="secondary">
                      Editar
                    </Link>{" "}
                    <button className="danger" onClick={() => setDeleteTarget(t.name)}>
                      Apagar
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {deleteTarget && (
        <ConfirmModal
          title="Confirmar remoção"
          message={`Isto apaga o modelo "${deleteTarget}" -- não afeta nenhuma instância já lançada a partir dele.`}
          expectedText={deleteTarget}
          confirmLabel="Apagar"
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </>
  );
}
