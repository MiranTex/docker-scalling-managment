"use client";

import Loading from "@/components/Skeleton";
import Link from "next/link";
import { useEffect, useState } from "react";
import { api, envPath, statusBadge, type EnvironmentNetwork, type NetworkMember } from "@/lib/platformClient";

const KIND: Record<NetworkMember["kind"], string> = {
  service: "serviço",
  addon: "addon",
  job: "job",
  platform: "plataforma",
  other: "desconhecido",
};

export default function NetworkTab({ project, env }: { project: string; env: string }) {
  const [network, setNetwork] = useState<EnvironmentNetwork | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const load = () =>
      api<EnvironmentNetwork>(envPath(project, env, "network"))
        .then((n) => {
          setNetwork(n);
          setError(null);
        })
        .catch((e) => setError((e as Error).message));
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [project, env]);

  const others = network?.members.filter((m) => m.kind === "other") ?? [];

  return (
    <>
      <p className="muted">
        A rede deste ambiente. Só os seus serviços e addons estão nela, mais os três componentes da plataforma que
        precisam de chegar às apps. Nenhum outro ambiente ou projeto a vê.
      </p>
      {error && <div className="error">{error}</div>}
      {!network ? (
        <Loading />
      ) : (
        <>
          <p>
            <code>{network.name}</code>
            {network.subnet && (
              <span className="muted">
                {" "}
                · sub-rede {network.subnet}
                {network.gateway && ` · gateway ${network.gateway}`}
              </span>
            )}
          </p>
          {others.length > 0 && (
            <div className="error">
              Há {others.length} container(s) ligado(s) a esta rede que não pertencem ao ambiente nem à plataforma.
            </div>
          )}
          <table>
            <thead>
              <tr>
                <th>Container</th>
                <th>Tipo</th>
                <th>O quê</th>
                <th>Estado</th>
              </tr>
            </thead>
            <tbody>
              {network.members.map((m) => (
                <tr key={m.container}>
                  <td>
                    <code>{m.container}</code>
                  </td>
                  <td>
                    <span className={m.kind === "other" ? "badge failed" : "badge"}>{KIND[m.kind]}</span>
                  </td>
                  <td>
                    {m.kind === "service" && m.name ? (
                      <Link href={`/projects/${project}/${env}/services/${m.name}`}>{m.name}</Link>
                    ) : (
                      (m.name ?? "—")
                    )}
                  </td>
                  <td>{m.state ? <span className={statusBadge(m.state)}>{m.state}</span> : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </>
  );
}
