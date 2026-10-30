"use client";

import { authError } from "@/lib/errors";
import { useState } from "react";
import { useRouter } from "next/navigation";
import RecoveryCodes from "@/components/RecoveryCodes";
import TotpSetup from "@/components/TotpSetup";

// safeFrom: a página de onde o login foi pedido (?from=), só se for deste
// site -- um "//outro.site" ou "https://..." era um redirecionamento aberto.
function safeFrom(): string | null {
  const from = new URLSearchParams(window.location.search).get("from");
  return from && from.startsWith("/") && !from.startsWith("//") ? from : null;
}

// Os passos do login: a password; depois, com 2FA, o código -- ou, para um
// admin que ainda não o tem, a ativação (QR e primeiro código) e os códigos
// de recuperação, antes de entrar.
type Step =
  | { kind: "password" }
  | { kind: "code"; mfaToken: string }
  | { kind: "setup"; mfaToken: string; secret: string; uri: string }
  | { kind: "recovery"; codes: string[] };

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [step, setStep] = useState<Step>({ kind: "password" });
  const [code, setCode] = useState("");
  const [changed] = useState(() => typeof window !== "undefined" && new URLSearchParams(window.location.search).has("changed"));

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const res = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      const data = await res.json().catch(() => ({}));
      if (!res.ok) {
        setError(authError(data, "e-mail ou senha inválidos"));
        return;
      }
      if (data.mfa_required) {
        setCode("");
        setStep({ kind: "code", mfaToken: data.mfa_token });
        return;
      }
      if (data.mfa_setup_required) {
        const setup = await fetch("/api/auth/login/mfa/setup", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ mfaToken: data.mfa_token }),
        });
        const s = await setup.json().catch(() => ({}));
        if (!setup.ok) {
          setError(authError(s, "não foi possível começar a ativação do 2FA"));
          return;
        }
        setStep({ kind: "setup", mfaToken: data.mfa_token, secret: s.secret, uri: s.otpauth_uri });
        return;
      }
      enter();
    } finally {
      setLoading(false);
    }
  }

  function enter() {
    router.push(safeFrom() ?? "/dashboard");
    router.refresh();
  }

  // O pedido de 2FA expirou (ou o auth reiniciou): volta à password.
  function restart(data: unknown) {
    setError(authError(data, "o pedido expirou: entra outra vez com a password"));
    setPassword("");
    setStep({ kind: "password" });
  }

  async function handleCode(e: React.FormEvent) {
    e.preventDefault();
    if (step.kind !== "code") return;
    setError(null);
    setLoading(true);
    try {
      const res = await fetch("/api/auth/login/mfa", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ mfaToken: step.mfaToken, code: code.trim(), email }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        if (data?.error?.code === "mfa_expired") return restart(data);
        setError(authError(data, "código inválido"));
        return;
      }
      enter();
    } finally {
      setLoading(false);
    }
  }

  async function confirmSetup(c: string): Promise<string | null> {
    if (step.kind !== "setup") return null;
    const res = await fetch("/api/auth/login/mfa/confirm", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ mfaToken: step.mfaToken, code: c, email }),
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      if (data?.error?.code === "mfa_expired") {
        restart(data);
        return null;
      }
      return authError(data, "código inválido");
    }
    setStep({ kind: "recovery", codes: data.recoveryCodes });
    return null;
  }

  if (step.kind === "setup") {
    return (
      <main className="page">
        <div className="card">
          <h1>Ativar a verificação em dois passos</h1>
          <p className="muted">Os administradores da plataforma entram com a password e um código do telemóvel.</p>
          <TotpSetup uri={step.uri} secret={step.secret} onConfirm={confirmSetup} confirmLabel="Ativar e entrar" />
        </div>
      </main>
    );
  }

  if (step.kind === "recovery") {
    return (
      <main className="page">
        <div className="card">
          <h1>2FA ativo</h1>
          <RecoveryCodes codes={step.codes} onDone={enter} doneLabel="Já os guardei, entrar" />
        </div>
      </main>
    );
  }

  if (step.kind === "code") {
    return (
      <main className="page">
        <div className="card">
          <h1>Código de verificação</h1>
          {error && <div className="error">{error}</div>}
          <form onSubmit={handleCode}>
            <label htmlFor="code">O código de 6 dígitos da app de autenticação</label>
            <input
              id="code"
              autoComplete="one-time-code"
              required
              autoFocus
              value={code}
              onChange={(e) => setCode(e.target.value)}
            />
            <button className="primary" type="submit" disabled={loading}>
              {loading ? "A verificar..." : "Entrar"}
            </button>
          </form>
          <p className="muted" style={{ marginTop: "1rem" }}>
            Sem o telemóvel? Escreve um dos códigos de recuperação (xxxx-xxxx-xxxx).
          </p>
        </div>
      </main>
    );
  }

  return (
    <main className="page">
      <div className="card">
        <h1>Entrar</h1>
        {changed && !error && <div className="notice">Password trocada. Entra com a nova.</div>}
        {error && <div className="error">{error}</div>}
        <form onSubmit={handleSubmit}>
          <label htmlFor="email">E-mail</label>
          <input
            id="email"
            type="email"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <label htmlFor="password">Senha</label>
          <input
            id="password"
            type="password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <button className="primary" type="submit" disabled={loading}>
            {loading ? "A entrar..." : "Entrar"}
          </button>
        </form>
        <p className="muted" style={{ marginTop: "1rem" }}>
          Sem conta? Pede-a a um administrador da plataforma.
        </p>
      </div>
    </main>
  );
}
