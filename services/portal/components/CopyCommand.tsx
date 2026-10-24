"use client";

import { useState } from "react";

// Um comando para colar no terminal, com botão de copiar.
export default function CopyCommand({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Sem permissão para o clipboard: o comando continua visível para copiar à mão.
    }
  }

  return (
    <div className="row" style={{ alignItems: "center", gap: 6, flexWrap: "nowrap" }}>
      <code style={{ whiteSpace: "nowrap" }}>{command}</code>
      <button className="secondary" onClick={copy} title="Copiar comando">
        {copied ? "Copiado" : "Copiar"}
      </button>
    </div>
  );
}
