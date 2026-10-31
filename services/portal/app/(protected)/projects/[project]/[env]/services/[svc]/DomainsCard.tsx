"use client";

import { useEffect, useState } from "react";
import Modal from "@/components/Modal";
import { api, statusBadge, type Domain } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";

// DomainsCard serve os domínios próprios de um serviço e os de um bucket
// público: base é o caminho da coleção na API (.../domains).
export default function DomainsCard({
  base,
  owner = "este serviço",
  placeholder = "loja.pt",
  title = "Domínios próprios",
}: {
  base: string;
  owner?: string;
  placeholder?: string;
  title?: string;
}) {
  const canEdit = useCan("developer");
  const [domains, setDomains] = useState<Domain[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [hostname, setHostname] = useState("");
  const [adding, setAdding] = useState(false);
  const path = (...rest: string[]) => [base, ...rest.map(encodeURIComponent)].join("/");

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
  }, [base]);

  async function add() {
    setError(null);
    try {
      await api(path(), { method: "POST", body: JSON.stringify({ hostname: hostname.trim() }) });
      setHostname("");
      setAdding(false);
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
    if (!confirm(`Remover ${host}? Deixa de responder por ${owner}.`)) return;
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
      <div className="page-header">
        <h1>{title}</h1>
        {canEdit && (
          <button className="primary" onClick={() => setAdding(true)}>
            Acrescentar domínio
          </button>
        )}
      </div>
      <p className="muted">
        Um domínio só passa a responder (e só pede certificado HTTPS) depois de o DNS apontar para este servidor. Os
        pendentes são verificados de novo a cada 2 minutos.
      </p>
      {!adding && error && <div className="error">{error}</div>}
      {domains && domains.length === 0 && <p className="muted">Sem domínios próprios.</p>}
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
                  {canEdit && <div className="actions">
                    {d.status !== "active" && (
                      <button className="secondary" onClick={() => verify(d.hostname)}>
                        Verificar agora
                      </button>
                    )}
                    <button className="danger" onClick={() => remove(d.hostname)}>
                      Remover
                    </button>
                  </div>}
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
      {adding && (
        <Modal title="Acrescentar domínio" onClose={() => setAdding(false)}>
          {error && <div className="error">{error}</div>}
          <label htmlFor="domain-host">Domínio</label>
          <input id="domain-host" value={hostname} onChange={(e) => setHostname(e.target.value)} placeholder={placeholder} onKeyDown={(e) => e.key === "Enter" && hostname.trim() && add()} />
          <p className="muted">Fica à espera até o DNS apontar para este servidor (as instruções aparecem na lista).</p>
          <button className="primary" disabled={!hostname.trim()} onClick={add}>
            Acrescentar
          </button>
        </Modal>
      )}
    </div>
  );
}
