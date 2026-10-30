"use client";

import { useCallback, useEffect, useRef, useState } from "react";

const EXIT_MS = 150;
const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

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
  const dialogRef = useRef<HTMLDivElement>(null);
  const [closing, setClosing] = useState(false);
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  // Deixa a animação de saída correr antes de o pai desmontar o modal.
  const requestClose = useCallback(() => {
    setClosing((already) => {
      if (!already) setTimeout(() => onCloseRef.current(), EXIT_MS);
      return true;
    });
  }, []);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const dialog = dialogRef.current;
    const first = dialog?.querySelector<HTMLElement>("input, select, textarea") ?? dialog?.querySelector<HTMLElement>(FOCUSABLE);
    first?.focus();
    return () => previous?.focus?.();
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape" && dismissable) {
        requestClose();
        return;
      }
      if (e.key !== "Tab") return;
      const items = dialogRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE);
      if (!items || items.length === 0) return;
      const firstEl = items[0];
      const lastEl = items[items.length - 1];
      if (e.shiftKey && document.activeElement === firstEl) {
        e.preventDefault();
        lastEl.focus();
      } else if (!e.shiftKey && document.activeElement === lastEl) {
        e.preventDefault();
        firstEl.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [dismissable, requestClose]);

  return (
    <div
      className={`modal-backdrop${closing ? " closing" : ""}`}
      onMouseDown={(e) => dismissable && e.target === e.currentTarget && requestClose()}
    >
      <div ref={dialogRef} className="modal" role="dialog" aria-modal="true" aria-label={title} style={{ maxWidth: width }}>
        <div className="modal-header">
          <h2>{title}</h2>
          <button className="secondary" aria-label="Fechar" onClick={requestClose}>
            ✕
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
