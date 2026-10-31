"use client";

import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { api, projectPath, type DeployToken, type Environment } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";

export default function DeployTokensCard({
  project,
  environments,
  apiUrl,
}: {
  project: string;
  environments: Environment[];
  apiUrl?: string;
}) {
  const canEdit = useCan("developer");
  const [tokens, setTokens] = useState<DeployToken[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [env, setEnv] = useState("");
  const [created, setCreated] = useState<{ token: string; env: string } | null>(null);
  const [creating, setCreating] = useState(false);

  async function load() {
    try {
      setTokens(await api<DeployToken[]>(projectPath(project, "deploy-tokens")));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project]);

  async function create() {
    setError(null);
    try {
      const res = await api<DeployToken & { token: string }>(projectPath(project, "deploy-tokens"), {
        method: "POST",
        body: JSON.stringify({ name: name.trim(), environment: env }),
      });
      setCreated({ token: res.token, env: env || environments[0]?.slug || "production" });
      setName("");
      setCreating(false);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function revoke(id: string) {
    if (!confirm("Revogar este token? O CI que o usa deixa de conseguir fazer deploy.")) return;
    try {
      await api(projectPath(project, "deploy-tokens", id), { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const envName = (id?: string) => environments.find((e) => e.id === id)?.slug ?? "todos";
  const base = apiUrl || "https://api.<PUBLIC_BASE_DOMAIN>";

  return (
    <div className="card wide">
      <div className="page-header">
        <h2>Tokens de deploy (CI)</h2>
        {canEdit && (
          <button className="primary" onClick={() => setCreating(true)}>
            Novo token
          </button>
        )}
      </div>
      <p className="muted">
        Um token só consegue fazer deploy e ver o estado dos deploys deste projeto -- não mexe em configuração, segredos
        nem dados.
      </p>
      {!creating && error && <div className="error">{error}</div>}
      {created && (
        <div className="notice">
          <p>
            <strong>Copia agora -- não volta a ser mostrado:</strong>
          </p>
          <code className="key">{created.token}</code>
          <p className="muted">Exemplo no CI (falha se o deploy falhar):</p>
          <pre className="generated">{`curl --fail-with-body -X POST \\
  -H "Authorization: Bearer $BASE_STACK_DEPLOY_TOKEN" \\
  -H "Content-Type: application/json" \\
  -d "{\\"image\\": \\"ghcr.io/eu/app:$GIT_SHA\\"}" \\
  "${base}/v1/projects/${project}/environments/${created.env}/services/<serviço>/deployments?wait=true"`}</pre>
          <button className="secondary" onClick={() => setCreated(null)}>
            Já copiei
          </button>
        </div>
      )}
      {tokens && tokens.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Nome</th>
              <th>Ambiente</th>
              <th>Último uso</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {tokens.map((t) => (
              <tr key={t.id}>
                <td>
                  {t.name} <code>{t.prefix}…</code>
                </td>
                <td>{envName(t.environmentId)}</td>
                <td>{t.lastUsedAt ? new Date(t.lastUsedAt).toLocaleString("pt-PT") : "nunca"}</td>
                <td>
                  {t.revokedAt ? (
                    <span className="badge revoked">revogado</span>
                  ) : (
                    canEdit && (
                    <button className="danger" onClick={() => revoke(t.id)}>
                      Revogar
                    </button>
                    )
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {creating && (
        <Modal title="Novo token de deploy" onClose={() => setCreating(false)}>
          {error && <div className="error">{error}</div>}
          <div className="form-grid">
        <div>
          <label htmlFor="tok-name">Nome</label>
          <input id="tok-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="github-actions" />
        </div>
        <div>
          <label htmlFor="tok-env">Ambiente</label>
          <select id="tok-env" value={env} onChange={(e) => setEnv(e.target.value)}>
            <option value="">todos</option>
            {environments.map((e) => (
              <option key={e.id} value={e.slug}>
                só {e.slug}
              </option>
            ))}
          </select>
        </div>
      </div>
          <button className="primary" disabled={!name.trim()} onClick={create}>
            Criar token
          </button>
        </Modal>
      )}
    </div>
  );
}
