"use client";

import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { api } from "@/lib/platformClient";

// A CLI (bs login / docker push) pediu acesso: quem está com sessão
// confere o código com o do terminal e autoriza. A key criada é desta
// pessoa, com o scope "platform", e aparece em Tokens (revoga-se lá).
const KEY_TTL_SECONDS = 90 * 24 * 3600;

type Pending = { name: string; expiresAt: string; approved: boolean; caHash?: string };

export default function CLILoginClient({ code }: { code: string }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [state, setState] = useState<"idle" | "busy" | "done">("idle");

  useEffect(() => {
    if (!code) {
      setError("Falta o código. Corre o login outra vez no terminal.");
      return;
    }
    api<Pending>(`cli/login/${encodeURIComponent(code)}`)
      .then(setPending)
      .catch((e) => setError((e as Error).message));
  }, [code]);

  async function approve() {
    setState("busy");
    setError(null);
    try {
      const res = await fetch("/api/tokens", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ scopes: ["platform"], ttl_seconds: KEY_TTL_SECONDS }),
      });
      const created = await res.json().catch(() => ({}));
      if (!res.ok || !created.key) throw new Error(created.message ?? "não foi possível criar a API key");
      await api(`cli/login/${encodeURIComponent(code)}/approve`, { method: "POST", body: JSON.stringify({ apiKey: created.key }) });
      setState("done");
    } catch (e) {
      setError((e as Error).message);
      setState("idle");
    }
  }

  return (
    <>
      <PageHeader title="Autorizar a CLI" description="Um terminal pediu para entrar na plataforma com a tua conta." />
      <div className="card wide" style={{ maxWidth: 560 }}>
        {error && <div className="error">{error}</div>}
        {state === "done" ? (
          <>
            <div className="notice">Autorizado. Podes voltar ao terminal (e fechar esta página).</div>
            <p className="muted">A API key criada aparece em Tokens, válida por 90 dias -- revoga-a lá se perderes este computador.</p>
          </>
        ) : (
          pending && (
            <>
              <p>Confirma que o código é o mesmo que aparece no terminal:</p>
              <p style={{ fontSize: "2rem", fontWeight: 700, letterSpacing: "0.15em", textAlign: "center", margin: "1rem 0" }}>
                <code>{code}</code>
              </p>
              <p>
                Computador: <strong>{pending.name || "?"}</strong>
              </p>
              <p className="muted">
                Se não foste tu a pedir isto no terminal, não autorizes: daria acesso a todos os teus projetos.
              </p>
              {pending.caHash && (
                <p className="muted" style={{ wordBreak: "break-all" }}>
                  Se o terminal perguntar se deve confiar na CA do registry, a impressão digital certa é:{" "}
                  <code>{pending.caHash}</code>
                </p>
              )}
              <button className="primary" disabled={state === "busy"} onClick={approve}>
                {state === "busy" ? "A autorizar..." : "Autorizar"}
              </button>
            </>
          )
        )}
      </div>
    </>
  );
}
