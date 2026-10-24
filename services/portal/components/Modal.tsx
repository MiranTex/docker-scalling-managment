"use client";

import { useEffect } from "react";

export default function Modal({
  title,
  onClose,
  children,
  width = 520,
  dismissable = true,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  width?: number;
  // dismissable=false: só fecha pelo ✕ -- num terminal o Escape é uma
  // tecla como as outras, e um clique fora fechava a sessão sem querer.
  dismissable?: boolean;
}) {
  useEffect(() => {
    if (!dismissable) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose, dismissable]);

  return (
    <div className="modal-backdrop" onMouseDown={(e) => dismissable && e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-modal="true" aria-label={title} style={{ maxWidth: width }}>
        <div className="modal-header">
          <h2>{title}</h2>
          <button className="secondary" aria-label="Fechar" onClick={onClose}>
            ✕
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
