"use client";

import { useState } from "react";
import Link from "next/link";

export default function RegisterPage() {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const res = await fetch("/api/auth/register", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email, password }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.message ?? "erro ao criar conta");
        return;
      }
      setDone(true);
    } finally {
      setLoading(false);
    }
  }

  if (done) {
    return (
      <main className="page">
        <div className="card">
          <h1>Conta criada</h1>
          <p className="notice">
            Já podes entrar com a tua senha. O serviço de auth ainda não envia
            e-mail de verdade nesta fase de demo — o token de verificação de
            e-mail sai só no log estruturado do serviço.
          </p>
          <Link href="/login">Ir para o login</Link>
        </div>
      </main>
    );
  }

  return (
    <main className="page">
      <div className="card">
        <h1>Criar conta</h1>
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
          <label htmlFor="password">Senha (mínimo 8 caracteres)</label>
          <input
            id="password"
            type="password"
            required
            minLength={8}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <button className="primary" type="submit" disabled={loading}>
            {loading ? "A criar..." : "Criar conta"}
          </button>
        </form>
        <p className="muted" style={{ marginTop: "1rem" }}>
          Já tens conta? <Link href="/login">Entrar</Link>
        </p>
      </div>
    </main>
  );
}
