"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";

// safeFrom: a página de onde o login foi pedido (?from=), só se for deste
// site -- um "//outro.site" ou "https://..." era um redirecionamento aberto.
function safeFrom(): string | null {
  const from = new URLSearchParams(window.location.search).get("from");
  return from && from.startsWith("/") && !from.startsWith("//") ? from : null;
}

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
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
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        setError(data.message ?? "e-mail ou senha inválidos");
        return;
      }
      router.push(safeFrom() ?? "/dashboard");
      router.refresh();
    } finally {
      setLoading(false);
    }
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
