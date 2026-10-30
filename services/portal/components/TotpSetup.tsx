"use client";

import QRCode from "qrcode";
import { useEffect, useState } from "react";

// Ativar o 2FA: o QR para a app (Google Authenticator, 1Password, Authy...)
// e o segredo em texto para quem não pode ler o QR; depois o primeiro
// código confirma. O QR é desenhado aqui no browser -- o segredo não sai
// para mais lado nenhum.
export default function TotpSetup({
  uri,
  secret,
  onConfirm,
  confirmLabel = "Ativar",
}: {
  uri: string;
  secret: string;
  // Devolve a mensagem de erro, ou null se correu bem.
  onConfirm: (code: string) => Promise<string | null>;
  confirmLabel?: string;
}) {
  const [qr, setQr] = useState<string | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    QRCode.toDataURL(uri, { margin: 1, width: 200 }).then(setQr, () => setQr(null));
  }, [uri]);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      setError(await onConfirm(code.trim()));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <p>1. Lê este código com a app de autenticação do telemóvel:</p>
      {qr && (
        <img src={qr} alt="Código QR para a app de autenticação" width={200} height={200} style={{ display: "block", margin: "0.5rem 0" }} />
      )}
      <p className="muted">Sem câmara? Escreve na app esta chave:</p>
      <code className="key">{secret.match(/.{1,4}/g)?.join(" ")}</code>
      <form onSubmit={submit}>
        {error && <div className="error">{error}</div>}
        <label htmlFor="totp-code">2. Escreve o código de 6 dígitos que a app mostra</label>
        <input
          id="totp-code"
          inputMode="numeric"
          autoComplete="one-time-code"
          pattern="[0-9 ]{6,7}"
          required
          autoFocus
          value={code}
          onChange={(e) => setCode(e.target.value)}
        />
        <button className="primary" type="submit" disabled={busy}>
          {busy ? "A verificar..." : confirmLabel}
        </button>
      </form>
    </div>
  );
}
