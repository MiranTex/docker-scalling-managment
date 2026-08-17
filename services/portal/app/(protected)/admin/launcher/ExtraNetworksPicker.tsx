"use client";

import { useEffect, useState } from "react";
import type { DockerNetwork } from "@/lib/launcherClient";

// <select multiple> de redes ADICIONAIS -- ao contrário de NetworkPicker
// (uma só, a rede em que o container nasce), aqui dá para escolher mais
// do que uma (ex: a rede da app E "observability-net" ao mesmo tempo,
// Ctrl/Cmd+clique para selecionar várias). "exclude" tira da lista a rede
// já escolhida como primária em NetworkPicker, pra não aparecer duplicada.
export default function ExtraNetworksPicker({
  id,
  exclude,
  value,
  onChange,
}: {
  id: string;
  exclude?: string;
  value: string[];
  onChange: (networks: string[]) => void;
}) {
  const [networks, setNetworks] = useState<DockerNetwork[] | null>(null);

  useEffect(() => {
    fetch("/api/admin/launcher/networks")
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (data) setNetworks(data);
      })
      .catch(() => {
        // Só desativa o seletor -- lançar continua a funcionar sem redes
        // adicionais.
      });
  }, []);

  const options = (networks ?? []).filter((n) => n.Name !== exclude);
  if (options.length === 0) return null;

  return (
    <select
      id={id}
      multiple
      value={value}
      onChange={(e) => onChange(Array.from(e.target.selectedOptions).map((o) => o.value))}
      size={Math.min(options.length, 5)}
    >
      {options.map((n) => (
        <option key={n.Id} value={n.Name}>
          {n.Name}
        </option>
      ))}
    </select>
  );
}
