"use client";

import { authError } from "@/lib/errors";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import RecoveryCodes from "@/components/RecoveryCodes";
import TotpSetup from "@/components/TotpSetup";
import type { MFAStatus } from "@/lib/authClient";

export default function AccountPage() {
  const router = useRouter();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (next !== confirm) {
      setError("A password nova e a confirmação não são iguais.");
      return;
    }
    setSaving(true);
    try {
      const res = await fetch("/api/account/password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ currentPassword: current, newPassword: next }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(authError(data, "não foi possível trocar a password"));
        return;
      }
      // As sessões foram terminadas: entra de novo com a password nova.
      router.push("/login?changed=1");
      router.refresh();
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <PageHeader title="A minha conta" description="A password e a verificação em dois passos." />
      <div className="card" style={{ maxWidth: 480 }}>
        <h2 style={{ marginTop: 0 }}>Password</h2>
        <p className="muted">Depois de a trocar, entras de novo em todos os sítios.</p>
        {error && <div className="error">{error}</div>}
        <form onSubmit={save}>
          <label htmlFor="current">Password atual</label>
          <input id="current" type="password" required autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
          <label htmlFor="next">Password nova (mínimo 8 caracteres)</label>
          <input id="next" type="password" required minLength={8} autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
          <label htmlFor="confirm">Confirmar a password nova</label>
          <input id="confirm" type="password" required minLength={8} autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          <button className="primary" type="submit" disabled={saving}>
            {saving ? "A guardar..." : "Trocar a password"}
          </button>
        </form>
      </div>
      <MFASection />
    </>
  );
}

// A verificação em dois passos (TOTP): ativar, códigos de recuperação
// novos, desligar (não para os administradores, que são obrigados a ter).
function MFASection() {
  const [status, setStatus] = useState<MFAStatus | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const [setup, setSetup] = useState<{ secret: string; uri: string } | null>(null);
  const [codes, setCodes] = useState<string[] | null>(null);
  const [action, setAction] = useState<"none" | "regenerate" | "disable">("none");
  const [input, setInput] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function load() {
    const res = await fetch("/api/account/mfa");
    if (res.status === 404) {
      setUnavailable(true);
      return;
    }
    if (res.ok) setStatus(await res.json());
  }

  useEffect(() => {
    load();
  }, []);

  async function start() {
    setError(null);
    const res = await fetch("/api/account/mfa/setup", { method: "POST" });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      setError(authError(data, "não foi possível começar a ativação"));
      return;
    }
    setSetup({ secret: data.secret, uri: data.otpauth_uri });
  }

  async function confirm(code: string): Promise<string | null> {
    const res = await fetch("/api/account/mfa/confirm", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) return authError(data, "código inválido");
    setSetup(null);
    setCodes(data.recovery_codes);
    return null;
  }

  async function submitAction(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      if (action === "regenerate") {
        const res = await fetch("/api/account/mfa/recovery-codes", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ code: input.trim() }),
        });
        const data = await res.json().catch(() => ({}));
        if (!res.ok) {
          setError(authError(data, "código inválido"));
          return;
        }
        setCodes(data.recovery_codes);
      } else {
        const res = await fetch("/api/account/mfa", {
          method: "DELETE",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ password: input }),
        });
        if (!res.ok) {
          const data = await res.json().catch(() => ({}));
          setError(authError(data, "não foi possível desligar o 2FA"));
          return;
        }
      }
      setAction("none");
      setInput("");
      await load();
    } finally {
      setBusy(false);
    }
  }

  if (unavailable) return null;

  return (
    <div className="card" style={{ maxWidth: 480, marginTop: "1rem" }}>
      <h2 style={{ marginTop: 0 }}>Verificação em dois passos</h2>
      {error && <div className="error">{error}</div>}
      {codes ? (
        <RecoveryCodes codes={codes} onDone={() => setCodes(null)} />
      ) : setup ? (
        <>
          <TotpSetup uri={setup.uri} secret={setup.secret} onConfirm={confirm} />
          <button className="secondary" style={{ marginTop: "0.5rem" }} onClick={() => setSetup(null)}>
            Cancelar
          </button>
        </>
      ) : !status ? (
        <p className="muted">A carregar...</p>
      ) : !status.enabled ? (
        <>
          <p className="muted">
            Além da password, um código da app de autenticação do telemóvel ao entrar.
            {status.required && " Obrigatório para administradores da plataforma."}
          </p>
          <button className="primary" onClick={start}>
            Ativar 2FA
          </button>
        </>
      ) : (
        <>
          <p>
            <span className="badge active">Ativo</span>{" "}
            <span className="muted">
              {status.recovery_codes_left} código{status.recovery_codes_left === 1 ? "" : "s"} de recuperação por usar
            </span>
          </p>
          {action === "none" ? (
            <div className="row">
              <button className="secondary" onClick={() => setAction("regenerate")}>
                Códigos de recuperação novos
              </button>
              {!status.required && (
                <button className="secondary" onClick={() => setAction("disable")}>
                  Desligar 2FA
                </button>
              )}
            </div>
          ) : (
            <form onSubmit={submitAction}>
              {action === "regenerate" ? (
                <label htmlFor="mfa-input">Um código da app (os códigos antigos deixam de servir)</label>
              ) : (
                <label htmlFor="mfa-input">A tua password, para desligar o 2FA</label>
              )}
              <input
                id="mfa-input"
                type={action === "disable" ? "password" : "text"}
                autoComplete={action === "disable" ? "current-password" : "one-time-code"}
                required
                autoFocus
                value={input}
                onChange={(e) => setInput(e.target.value)}
              />
              <div className="row">
                <button className="primary" type="submit" disabled={busy}>
                  {action === "regenerate" ? "Gerar códigos" : "Desligar"}
                </button>
                <button
                  className="secondary"
                  type="button"
                  onClick={() => {
                    setAction("none");
                    setInput("");
                    setError(null);
                  }}
                >
                  Cancelar
                </button>
              </div>
            </form>
          )}
        </>
      )}
    </div>
  );
}
