"use client";

import { useEffect, useState } from "react";
import { api, servicePath, statusBadge, type Domain } from "@/lib/platformClient";

export default function DomainsCard({ project, env, svc }: { project: string; env: string; svc: string }) {
  const [domains, setDomains] = useState<Domain[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hostname, setHostname] = useState("");
  const path = (...rest: string[]) => servicePath(project, env, svc, "domains", ...rest);

  async function load() {
    try {
      setDomains(await api<Domain[]>(path()));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, [project, env, svc]);

  async function add() {
    setError(null);
    try {
      await api(path(), { method: "POST", body: JSON.stringify({ hostname: hostname.trim() }) });
      setHostname("");
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function verify(host: string) {
    try {
      await api(path(host, "verify"), { method: "POST" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function remove(host: string) {
    if (!confirm(`Remover ${host}? Deixa de responder por este serviço.`)) return;
    try {
      await api(path(host), { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const sample = domains?.[0];

  return (
    <div className="card full">
      <h1>Domínios próprios</h1>
      <p className="muted">
        Um domínio só passa a responder (e só pede certificado HTTPS) depois de o DNS apontar para este servidor. Os
        pendentes são verificados de novo a cada 2 minutos.
      </p>
      {error && <div className="error">{error}</div>}
      {domains && domains.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Domínio</th>
              <th>Estado</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {domains.map((d) => (
              <tr key={d.hostname}>
                <td>
                  {d.status === "active" ? (
                    <a href={`https://${d.hostname}`} target="_blank" rel="noreferrer">
                      {d.hostname}
                    </a>
                  ) : (
                    d.hostname
                  )}
                </td>
                <td>
                  <span className={statusBadge(d.status === "failed" ? "pending" : d.status)}>
                    {d.status === "active" ? "ativo" : "à espera do DNS"}
                  </span>
                  {d.error && <div className="muted">{d.error}</div>}
                </td>
                <td>
                  <div className="actions">
                    {d.status !== "active" && (
                      <button className="secondary" onClick={() => verify(d.hostname)}>
                        Verificar agora
                      </button>
                    )}
                    <button className="danger" onClick={() => remove(d.hostname)}>
                      Remover
                    </button>
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {sample && (sample.expectedIps.length > 0 || sample.expectedCname) && (
        <div className="notice">
          No teu fornecedor de DNS, cria para cada domínio:
          <ul>
            {sample.expectedIps.map((ip) => (
              <li key={ip}>
                registo <strong>{ip.includes(":") ? "AAAA" : "A"}</strong> → <code>{ip}</code>
              </li>
            ))}
            {sample.expectedCname && (
              <li>
                ou (subdomínios) <strong>CNAME</strong> → <code>{sample.expectedCname}</code>
              </li>
            )}
          </ul>
        </div>
      )}
      <div className="row">
        <input value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder="loja.pt" />
        <button className="secondary" disabled={!hostname.trim()} onClick={add}>
          Adicionar
        </button>
      </div>
    </div>
  );
}
