"use client";

import { useEffect, useState } from "react";
import type { DockerNetwork } from "@/lib/launcherClient";

// Seletor de rede Docker reaproveitado em /admin/launcher (lançar
// instância) e /admin/autoscaler (criar grupo) -- lista as redes já
// existentes (ver services/launcher/internal/dockerclient.ListNetworks)
// e permite criar uma nova sem saltar para o /admin/launcher só para
// isso. Vazio = "usa a rede definida no modelo".
export default function NetworkPicker({
  id,
  value,
  onChange,
}: {
  id: string;
  value: string;
  onChange: (network: string) => void;
}) {
  const [networks, setNetworks] = useState<DockerNetwork[] | null>(null);
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");
  const [error, setError] = useState<string | null>(null);
  // manualEntry: escrever o nome à mão em vez de escolher da lista -- útil
  // quando a rede já existe (ex: "observability-net", criada por
  // services/monitoring) mas ainda não apareceu no <select> (lista só
  // atualiza no load da página) ou só se sabe o nome de cor. O launcher
  // continua a validar no servidor que a rede é mesmo gerida por esta
  // plataforma (base-stack.managed) -- isto é só um atalho de UI, não
  // contorna essa validação.
  const [manualEntry, setManualEntry] = useState(false);

  async function loadNetworks() {
    try {
      const res = await fetch("/api/admin/launcher/networks");
      if (res.ok) setNetworks(await res.json());
    } catch {
      // A lista só ajuda a preencher o <select> -- se falhar, ainda dá
      // para escrever o nome da rede à mão.
    }
  }

  useEffect(() => {
    loadNetworks();
  }, []);

  async function handleCreate() {
    const name = newName.trim();
    if (!name) return;
    setError(null);
    try {
      const res = await fetch("/api/admin/launcher/networks", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name }),
      });
      const data = await res.json();
      if (!res.ok) {
        setError(data.message ?? "erro ao criar rede");
        return;
      }
      onChange(name);
      setNewName("");
      setCreating(false);
      await loadNetworks();
    } catch {
      setError("erro ao criar rede");
    }
  }

  if (creating) {
    return (
      <div className="row" style={{ gap: "0.5rem" }}>
        {error && <div className="error">{error}</div>}
        <input
          type="text"
          value={newName}
          onChange={(e) => setNewName(e.target.value)}
          placeholder="nome-da-rede-nova"
          style={{ marginBottom: 0 }}
          autoFocus
        />
        <button className="secondary" onClick={handleCreate}>
          Criar
        </button>
        <button className="secondary" onClick={() => setCreating(false)}>
          Cancelar
        </button>
      </div>
    );
  }

  const hasList = networks && networks.length > 0;

  return (
    <div className="row" style={{ gap: "0.5rem" }}>
      {error && <div className="error">{error}</div>}
      {hasList && !manualEntry ? (
        <select id={id} value={value} onChange={(e) => onChange(e.target.value)} style={{ marginBottom: 0 }}>
          <option value="">-- nenhuma (usa a do modelo) --</option>
          {networks.map((n) => (
            <option key={n.Id} value={n.Name}>
              {n.Name}
            </option>
          ))}
        </select>
      ) : (
        <input
          id={id}
          type="text"
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="auth-net"
          style={{ marginBottom: 0 }}
        />
      )}
      {hasList && (
        <button className="secondary" onClick={() => setManualEntry((v) => !v)}>
          {manualEntry ? "Escolher da lista" : "Escrever nome"}
        </button>
      )}
      <button className="secondary" onClick={() => setCreating(true)}>
        + Nova rede
      </button>
    </div>
  );
}
