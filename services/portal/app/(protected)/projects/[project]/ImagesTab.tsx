"use client";

import Loading from "@/components/Skeleton";
import { useEffect, useState } from "react";
import CopyCommand from "@/components/CopyCommand";
import { api, formatBytes, projectPath, type ProjectImages } from "@/lib/platformClient";

// As imagens do projeto no registry da plataforma (só leitura). O nome
// completo é o que se usa no deploy.
export default function ImagesTab({ project }: { project: string }) {
  const [data, setData] = useState<ProjectImages | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function load() {
    try {
      setData(await api<ProjectImages>(projectPath(project, "images")));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [project]);

  return (
    <div className="card wide">
      <div className="page-header">
        <h2 style={{ margin: 0 }}>Imagens</h2>
        <button className="secondary" onClick={load}>
          Atualizar
        </button>
      </div>
      {error && <div className="error">{error}</div>}
      {!data && !error && <Loading />}
      {data && !data.enabled && <p className="muted">O registry da plataforma não está ligado nesta instalação.</p>}
      {data?.enabled && data.images.length === 0 && <HowToPush host={data.host!} project={project} />}
      {data?.enabled && data.images.length > 0 && (
        <>
          <table>
            <thead>
              <tr>
                <th>Imagem</th>
                <th>Tag</th>
                <th>Arquitetura</th>
                <th>Tamanho</th>
                <th>Criada</th>
              </tr>
            </thead>
            <tbody>
              {data.images.flatMap((img) =>
                img.tags.map((t) => (
                  <tr key={`${img.repository}:${t.tag}`}>
                    <td>
                      <CopyCommand command={`${img.reference}:${t.tag}`} />
                    </td>
                    <td>
                      <code title={t.digest}>{t.tag}</code>
                    </td>
                    <td>
                      {t.platforms.map((p) => (
                        <span key={p} className="badge" style={{ marginRight: 4 }}>
                          {p.replace(/^linux\//, "")}
                        </span>
                      ))}
                    </td>
                    <td>{formatBytes(t.size)}</td>
                    <td>{t.created ? new Date(t.created).toLocaleString("pt-PT") : "—"}</td>
                  </tr>
                )),
              )}
            </tbody>
          </table>
          <details style={{ marginTop: 16 }}>
            <summary className="muted">Como enviar uma imagem</summary>
            <HowToPush host={data.host!} project={project} />
          </details>
        </>
      )}
    </div>
  );
}

function HowToPush({ host, project }: { host: string; project: string }) {
  const ref = `${host}/${project}/minha-app:1.0`;
  return (
    <div>
      <p className="muted">
        Faz login com o teu email e uma API key (scope <code>platform</code>) como password, e envia
        para <code>
          {host}/{project}/...
        </code>
        :
      </p>
      <CopyCommand command={`docker login ${host}`} />
      <CopyCommand command={`docker build -t ${ref} .`} />
      <CopyCommand command={`docker push ${ref}`} />
    </div>
  );
}
