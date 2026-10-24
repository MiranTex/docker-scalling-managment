"use client";

import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef, useState } from "react";
import Modal from "@/components/Modal";

type Ticket = { url: string; container: string; debug: boolean };

// Terminal num container do projeto. Pede um bilhete ao launcher (pela
// API normal, via proxy do portal) e abre com ele o WebSocket diretamente
// no launcher -- as rotas do Next não fazem WebSocket. Mensagens binárias
// são o terminal; as de texto são controlo (resize).
export default function TerminalModal({
  title,
  path,
  replicas,
  onClose,
}: {
  title: string;
  // path da API do pedido de bilhete (ex: servicePath(..., "terminal")).
  path: string;
  // Réplicas a escolher (serviços); sem lista abre no único container.
  replicas?: string[];
  onClose: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const [replica, setReplica] = useState("");
  const [debug, setDebug] = useState(false);
  const [session, setSession] = useState(0); // muda para religar
  const [status, setStatus] = useState("A ligar...");
  const [noShell, setNoShell] = useState(false);

  useEffect(() => {
    let ws: WebSocket | null = null;
    let disposed = false;
    const cleanups: (() => void)[] = [];

    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]);
      if (disposed || !box.current) return;
      const term = new Terminal({ cursorBlink: true, fontSize: 13, convertEol: false, theme: { background: "#0b0e14" } });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(box.current);
      fit.fit();
      cleanups.push(() => term.dispose());

      setNoShell(false);
      setStatus("A pedir acesso...");
      const res = await fetch(`/api/platform/${path}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ replica, debug }),
      });
      const data = await res.json().catch(() => ({}));
      if (disposed) return;
      if (!res.ok) {
        if (data.error === "no_shell") setNoShell(true);
        setStatus(data.message ?? `erro ${res.status}`);
        return;
      }
      const ticket = data as Ticket;
      const url = ticket.url.startsWith("/")
        ? `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}${ticket.url}`
        : ticket.url;

      ws = new WebSocket(url);
      ws.binaryType = "arraybuffer";
      const encoder = new TextEncoder();
      const sendSize = () => {
        fit.fit();
        if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
      };
      ws.onopen = () => {
        setStatus(`Ligado a ${ticket.container}${ticket.debug ? " (modo debug)" : ""}`);
        sendSize();
        term.focus();
      };
      ws.onmessage = (e) => term.write(new Uint8Array(e.data as ArrayBuffer));
      ws.onclose = (e) => {
        if (disposed) return;
        setStatus(e.reason ? `Sessão fechada: ${e.reason}` : "Sessão fechada");
        term.write("\r\n\x1b[2m[sessão fechada]\x1b[0m\r\n");
      };
      const input = term.onData((d) => ws?.readyState === WebSocket.OPEN && ws.send(encoder.encode(d)));
      const observer = new ResizeObserver(() => sendSize());
      observer.observe(box.current);
      cleanups.push(() => input.dispose(), () => observer.disconnect());
    })().catch((e) => !disposed && setStatus((e as Error).message));

    return () => {
      disposed = true;
      ws?.close();
      cleanups.reverse().forEach((c) => c());
    };
  }, [path, session]);

  return (
    <Modal title={title} onClose={onClose} width={1000} dismissable={false}>
      <div className="row">
        {replicas && replicas.length > 1 && (
          <select value={replica} onChange={(e) => setReplica(e.target.value)} aria-label="Réplica">
            <option value="">qualquer réplica</option>
            {replicas.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
        )}
        <label className="checkbox" title="Container temporário ao lado do serviço, com as ferramentas do busybox -- para imagens sem shell">
          <input type="checkbox" checked={debug} onChange={(e) => setDebug(e.target.checked)} /> modo debug
        </label>
        <button className="secondary" onClick={() => setSession((n) => n + 1)}>
          Religar
        </button>
      </div>
      <p className="muted">{status}</p>
      {noShell && (
        <div className="notice">
          Esta imagem não tem shell.{" "}
          <button
            className="secondary"
            onClick={() => {
              setDebug(true);
              setSession((n) => n + 1);
            }}
          >
            Abrir em modo debug
          </button>
        </div>
      )}
      <div ref={box} style={{ height: 460, background: "#0b0e14", padding: 6, borderRadius: 6 }} />
    </Modal>
  );
}
