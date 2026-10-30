"use client";

import { copyText } from "@/lib/clipboard";

// Os códigos de recuperação, mostrados uma vez só: cada um serve uma vez
// em vez do código da app (telemóvel perdido).
export default function RecoveryCodes({ codes, onDone, doneLabel = "Já os guardei" }: { codes: string[]; onDone: () => void; doneLabel?: string }) {
  async function copy() {
    try {
      await copyText(codes.join("\n"), "Códigos copiados");
    } catch {
      // Continuam visíveis para copiar à mão.
    }
  }

  return (
    <div className="notice">
      <strong>Guarda estes códigos de recuperação agora. Não serão mostrados novamente.</strong>
      <p>Se perderes o telemóvel, cada um serve uma vez no lugar do código da app.</p>
      <pre className="generated">{codes.join("\n")}</pre>
      <div className="row">
        <button className="secondary" type="button" onClick={copy}>
          Copiar
        </button>
        <button className="primary" type="button" onClick={onDone}>
          {doneLabel}
        </button>
      </div>
    </div>
  );
}
