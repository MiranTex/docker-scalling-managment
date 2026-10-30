"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";

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
        setError(data.message ?? "não foi possível trocar a password");
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
      <PageHeader title="A minha conta" description="Trocar a password. Depois disso, entras de novo em todos os sítios." />
      <div className="card" style={{ maxWidth: 480 }}>
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
    </>
  );
}
