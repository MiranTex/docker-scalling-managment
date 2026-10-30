"use client";

import { useState } from "react";
import { copyText } from "@/lib/clipboard";
import { api, servicePath, type Service } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";

// Uma password forte para passar ao cliente (sem caracteres que se
// confundem ao ditar).
function generatePassword(): string {
  const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (b) => alphabet[b % alphabet.length]).join("");
}

// Proteger com password: o Traefik pede utilizador e password antes de a
// app responder, em todos os endereços do serviço. Para mostrar uma fase
// da app a um cliente -- não é a autenticação da app.
export default function BasicAuthCard({
  project,
  env,
  service,
  onSaved,
}: {
  project: string;
  env: string;
  service: Service;
  onSaved: () => Promise<void> | void;
}) {
  const canEdit = useCan("developer");
  const [editing, setEditing] = useState(false);
  const [user, setUser] = useState("");
  const [password, setPassword] = useState("");
  const [saved, setSaved] = useState<{ user: string; password: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const path = servicePath(project, env, service.slug, "basic-auth");
  const protectedBy = service.basicAuthUser;

  function startEditing() {
    setUser(protectedBy || "cliente");
    setPassword(generatePassword());
    setError(null);
    setSaved(null);
    setEditing(true);
  }

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setBusy(true);
    try {
      await api(path, { method: "PUT", body: JSON.stringify({ user: user.trim(), password }) });
      setSaved({ user: user.trim(), password });
      setEditing(false);
      await onSaved();
    } catch (err) {
      setError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function remove() {
    if (!confirm("Tirar a password? O serviço fica aberto a toda a gente.")) return;
    setError(null);
    try {
      await api(path, { method: "DELETE" });
      setSaved(null);
      await onSaved();
    } catch (err) {
      setError((err as Error).message);
    }
  }

  async function copy(text: string) {
    try {
      await copyText(text);
    } catch {
      // Continua visível para copiar à mão.
    }
  }

  return (
    <div className="card full">
      <h1>Proteger com password</h1>
      <p className="muted">
        Quem abrir o serviço tem de escrever um utilizador e uma password antes de ver a app -- por exemplo, para mostrar
        uma fase a um cliente. Vale para todos os endereços do serviço. Não é o login da app.
      </p>
      {error && <div className="error">{error}</div>}
      {saved && (
        <div className="notice">
          <strong>Passa estes dados ao cliente agora. A password não volta a ser mostrada.</strong>
          <p>
            Utilizador: <code>{saved.user}</code>
            <br />
            Password: <code>{saved.password}</code>
          </p>
          <div className="row">
            <button className="secondary" onClick={() => copy(`Utilizador: ${saved.user}\nPassword: ${saved.password}`)}>
              Copiar
            </button>
            <button className="secondary" onClick={() => setSaved(null)}>
              Fechar
            </button>
          </div>
        </div>
      )}
      <p>
        {protectedBy ? (
          <>
            <span className="badge active">Protegido</span> <span className="muted">utilizador {protectedBy}</span>
          </>
        ) : (
          <span className="muted">Aberto: qualquer pessoa com o endereço vê a app.</span>
        )}
      </p>
      {canEdit &&
        (editing ? (
          <form onSubmit={save}>
            <label htmlFor="ba-user">Utilizador</label>
            <input id="ba-user" required maxLength={64} value={user} onChange={(e) => setUser(e.target.value)} />
            <label htmlFor="ba-password">Password (mínimo 8 caracteres)</label>
            <div className="row" style={{ flexWrap: "nowrap" }}>
              <input
                id="ba-password"
                required
                minLength={8}
                maxLength={72}
                autoComplete="off"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
              <button className="secondary" type="button" onClick={() => setPassword(generatePassword())}>
                Gerar outra
              </button>
            </div>
            <div className="row">
              <button className="primary" type="submit" disabled={busy}>
                {busy ? "A guardar..." : protectedBy ? "Trocar password" : "Proteger"}
              </button>
              <button className="secondary" type="button" onClick={() => setEditing(false)}>
                Cancelar
              </button>
            </div>
          </form>
        ) : (
          <div className="row">
            <button className="secondary" onClick={startEditing}>
              {protectedBy ? "Trocar utilizador ou password" : "Proteger com password"}
            </button>
            {protectedBy && (
              <button className="danger" onClick={remove}>
                Tirar a password
              </button>
            )}
          </div>
        ))}
    </div>
  );
}
