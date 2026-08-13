"use client";

import { useEffect, useState } from "react";
import type { ContainerSummary, DockerNetwork, NetworkDetail } from "@/lib/launcherClient";
import ConfirmModal from "../database/ConfirmModal";

export default function NetworksAdminClient() {
  const [networks, setNetworks] = useState<DockerNetwork[] | null>(null);
  const [listError, setListError] = useState<string | null>(null);

  const [selectedName, setSelectedName] = useState<string | null>(null);
  const [detail, setDetail] = useState<NetworkDetail | null>(null);
  const [detailError, setDetailError] = useState<string | null>(null);

  const [containers, setContainers] = useState<ContainerSummary[] | null>(null);
  const [connectTarget, setConnectTarget] = useState("");
  const [connecting, setConnecting] = useState(false);
  const [connectionError, setConnectionError] = useState<string | null>(null);

  const [newName, setNewName] = useState("");
  const [createError, setCreateError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);

  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  async function loadNetworks() {
    try {
      const res = await fetch("/api/admin/launcher/networks");
      if (!res.ok) {
        setListError(`erro ${res.status} a listar redes`);
        return;
      }
      setNetworks(await res.json());
      setListError(null);
    } catch {
      setListError("erro a listar redes");
    }
  }

  async function loadContainers() {
    try {
      const res = await fetch("/api/admin/launcher/containers");
      if (res.ok) setContainers(await res.json());
    } catch {
      // A lista só ajuda a preencher o picker de "ligar container" -- se
      // falhar, o resto do ecrã continua a funcionar.
    }
  }

  useEffect(() => {
    loadNetworks();
    loadContainers();
  }, []);

  async function loadDetail(name: string) {
    setDetailError(null);
    setConnectionError(null);
    try {
      const res = await fetch(`/api/admin/launcher/networks/${encodeURIComponent(name)}`);
      const data = await res.json();
      if (!res.ok) {
        setDetailError(data.message ?? "erro a carregar rede");
        setDetail(null);
        return;
      }
      setDetail(data);
    } catch {
      setDetailError("erro a carregar rede");
      setDetail(null);
    }
  }

  function selectNetwork(name: string) {
    setSelectedName(name);
    setConnectTarget("");
    loadDetail(name);
  }

  async function refreshSelected() {
    if (selectedName) await loadDetail(selectedName);
    await loadContainers();
  }

  async function handleCreate() {
    setCreateError(null);
    const name = newName.trim();
    if (!name) {
      setCreateError("nome é obrigatório");
      return;
    }
    setCreating(true);
    try {
      const res = await fetch("/api/admin/launcher/networks", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      const data = await res.json();
      if (!res.ok) {
        setCreateError(data.message ?? "erro ao criar rede");
        return;
      }
      setNewName("");
      await loadNetworks();
      selectNetwork(name);
    } catch {
      setCreateError("erro ao criar rede");
    } finally {
      setCreating(false);
    }
  }

  async function handleDelete() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeleteTarget(null);
    setDeleteError(null);
    try {
      const res = await fetch(`/api/admin/launcher/networks/${encodeURIComponent(target)}`, { method: "DELETE" });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setDeleteError(data.message ?? "erro ao apagar rede");
        return;
      }
      if (selectedName === target) {
        setSelectedName(null);
        setDetail(null);
      }
      await loadNetworks();
    } catch {
      setDeleteError("erro ao apagar rede");
    }
  }

  async function handleConnect() {
    if (!selectedName || !connectTarget) return;
    setConnecting(true);
    setConnectionError(null);
    try {
      const res = await fetch(`/api/admin/launcher/networks/${encodeURIComponent(selectedName)}/connect`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ containerId: connectTarget }),
      });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setConnectionError(data.message ?? "erro ao ligar container");
        return;
      }
      setConnectTarget("");
      await refreshSelected();
    } catch {
      setConnectionError("erro ao ligar container");
    } finally {
      setConnecting(false);
    }
  }

  async function handleDisconnect(containerId: string) {
    if (!selectedName) return;
    setConnectionError(null);
    try {
      const res = await fetch(`/api/admin/launcher/networks/${encodeURIComponent(selectedName)}/disconnect`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ containerId }),
      });
      if (res.status !== 204) {
        const data = await res.json().catch(() => ({}));
        setConnectionError(data.message ?? "erro ao desligar container");
        return;
      }
      await refreshSelected();
    } catch {
      setConnectionError("erro ao desligar container");
    }
  }

  const connectedIds = new Set(detail?.containers.map((c) => c.containerId) ?? []);
  const connectableContainers = (containers ?? []).filter((c) => !connectedIds.has(c.containerId));

  return (
    <>
      <div className="card wide">
        <h1>Redes</h1>
        <p className="muted">
          Redes Docker definidas pelo utilizador -- é aqui que se escolhe/cria a rede usada ao
          lançar uma instância (<a href="/admin/launcher">/admin/launcher</a>) ou criar um grupo (
          <a href="/admin/autoscaler">/admin/autoscaler</a>). O Docker não permite editar
          nome/subnet/driver depois de criada -- o que se gere aqui é quais containers estão
          ligados a cada rede.
        </p>
        {listError && <div className="error">{listError}</div>}
        {deleteError && <div className="error">{deleteError}</div>}

        {!networks ? (
          <p className="muted">A carregar...</p>
        ) : networks.length === 0 ? (
          <p className="muted">Nenhuma rede definida pelo utilizador ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Nome</th>
                <th>Driver</th>
                <th>Scope</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {networks.map((n) => (
                <tr
                  key={n.Id}
                  onClick={() => selectNetwork(n.Name)}
                  style={{ cursor: "pointer", fontWeight: n.Name === selectedName ? "bold" : "normal" }}
                >
                  <td>
                    <code>{n.Name}</code>
                  </td>
                  <td>{n.Driver}</td>
                  <td>{n.Scope}</td>
                  <td>
                    <button
                      className="danger"
                      onClick={(e) => {
                        e.stopPropagation();
                        setDeleteTarget(n.Name);
                      }}
                    >
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
        <h1>Criar rede</h1>
        {createError && <div className="error">{createError}</div>}
        <label htmlFor="new-network-name">Nome</label>
        <input
          id="new-network-name"
          type="text"
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          placeholder="rede-do-projeto-x"
        />
        <div className="row">
          <button className="secondary" disabled={creating} onClick={handleCreate}>
            {creating ? "A criar..." : "Criar rede"}
          </button>
        </div>
      </div>

      {selectedName && (
        <div className="card wide">
          <h1>{selectedName}</h1>
          {detailError && <div className="error">{detailError}</div>}
          {connectionError && <div className="error">{connectionError}</div>}

          {detail && (
            <>
              {detail.containers.length === 0 ? (
                <p className="muted">Nenhum container ligado a esta rede.</p>
              ) : (
                <table>
                  <thead>
                    <tr>
                      <th>Container</th>
                      <th>Nome</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {detail.containers.map((c) => (
                      <tr key={c.containerId}>
                        <td>
                          <code>{c.containerId.slice(0, 12)}</code>
                        </td>
                        <td>{c.name}</td>
                        <td>
                          <button className="danger" onClick={() => handleDisconnect(c.containerId)}>
                            Desligar
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}

              <label htmlFor="connect-container" style={{ marginTop: "1rem" }}>
                Ligar container
              </label>
              <div className="row" style={{ gap: "0.5rem" }}>
                <select
                  id="connect-container"
                  value={connectTarget}
                  onChange={(e) => setConnectTarget(e.target.value)}
                  style={{ marginBottom: 0 }}
                >
                  <option value="">-- escolha um container --</option>
                  {connectableContainers.map((c) => (
                    <option key={c.containerId} value={c.containerId}>
                      {c.name} ({c.containerId.slice(0, 12)})
                    </option>
                  ))}
                </select>
                <button className="secondary" disabled={connecting || !connectTarget} onClick={handleConnect}>
                  {connecting ? "A ligar..." : "Ligar"}
                </button>
              </div>
            </>
          )}
        </div>
      )}

      {deleteTarget && (
        <ConfirmModal
          title="Confirmar remoção"
          message={`Isto apaga a rede "${deleteTarget}" -- só funciona se não tiver nenhum container ligado.`}
          expectedText={deleteTarget}
          confirmLabel="Apagar"
          onConfirm={handleDelete}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
    </>
  );
}
