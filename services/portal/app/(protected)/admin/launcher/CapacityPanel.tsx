"use client";

import type { Capacity } from "@/lib/launcherClient";
import { formatMemory } from "@/lib/instanceTypes";

// Barra de uso de um recurso. dangerous marca o ponto a partir do qual o
// consumo passa a ser um problema real -- só a memória o usa: CPU acima
// de 100% é overcommit inofensivo (containers ficam lentos), memória
// acima de 100% é o OOM-killer a matar containers à sorte.
function Bar({ used, total, dangerous }: { used: number; total: number; dangerous: boolean }) {
  const pct = total > 0 ? Math.min((used / total) * 100, 100) : 0;
  const over = total > 0 && used > total;
  const color = over || (dangerous && pct >= 90) ? "#c0392b" : pct >= 70 ? "#d68910" : "#27ae60";
  return (
    <div style={{ background: "rgba(127,127,127,0.2)", borderRadius: 4, height: 8, overflow: "hidden" }}>
      <div style={{ width: `${pct}%`, background: color, height: "100%" }} />
    </div>
  );
}

export default function CapacityPanel({ capacity }: { capacity: Capacity }) {
  const configured = capacity.hostVcpu > 0 || capacity.hostMemoryMb > 0;
  if (!configured) {
    return (
      <p className="muted">
        Alocado: {capacity.allocatedVcpu.toFixed(2)} vCPU · {formatMemory(capacity.allocatedMemoryMb)} em{" "}
        {capacity.containerCount} containers. Define <code>LAUNCHER_HOST_VCPU</code> e{" "}
        <code>LAUNCHER_HOST_MEMORY_MB</code> no launcher para comparar com a capacidade do host.
      </p>
    );
  }

  const memOver = capacity.hostMemoryMb > 0 && capacity.allocatedMemoryMb > capacity.hostMemoryMb;

  return (
    <div style={{ margin: "1rem 0", display: "grid", gap: "0.75rem" }}>
      <div>
        <div className="muted">
          CPU: {capacity.allocatedVcpu.toFixed(2)} / {capacity.hostVcpu} vCPU alocados
        </div>
        <Bar used={capacity.allocatedVcpu} total={capacity.hostVcpu} dangerous={false} />
      </div>
      <div>
        <div className="muted">
          Memória: {formatMemory(capacity.allocatedMemoryMb)} / {formatMemory(capacity.hostMemoryMb)} alocados
          {capacity.enforced ? "" : " (sem bloqueio)"}
        </div>
        <Bar used={capacity.allocatedMemoryMb} total={capacity.hostMemoryMb} dangerous />
      </div>
      {memOver && (
        <div className="error">
          Memória alocada acima da capacidade do host -- containers podem ser mortos pelo OOM-killer.
        </div>
      )}
      <p className="muted">
        Só a memória bloqueia lançamentos: CPU acima de 100% deixa tudo mais lento, memória acima de
        100% faz o kernel matar containers.
      </p>
    </div>
  );
}
