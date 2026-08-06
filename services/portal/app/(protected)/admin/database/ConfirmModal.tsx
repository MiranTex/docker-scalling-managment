"use client";

import { useState } from "react";

// Confirmação escrita (não um confirm() nativo) para ações demasiado
// destrutivas para um simples "OK/Cancelar" -- mesmo espírito do prompt
// "escreve 'sim'" que services/database/scripts/restore-pitr.sh já pede
// na consola, só que aqui é a UI que garante isso antes do pedido HTTP
// sequer sair do browser.
export default function ConfirmModal({
  title,
  message,
  expectedText,
  confirmLabel,
  onConfirm,
  onCancel,
}: {
  title: string;
  message: string;
  expectedText: string;
  confirmLabel: string;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const [typed, setTyped] = useState("");

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0, 0, 0, 0.5)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 1000,
      }}
    >
      <div className="card wide" style={{ maxWidth: 480 }}>
        <h1>{title}</h1>
        <p className="error">{message}</p>
        <p className="muted">
          Escreve <strong>{expectedText}</strong> para confirmar.
        </p>
        <input
          type="text"
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
          autoFocus
        />
        <div className="row">
          <button className="secondary" onClick={onCancel}>
            Cancelar
          </button>
          <button className="danger" disabled={typed !== expectedText} onClick={onConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
