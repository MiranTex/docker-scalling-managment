"use client";

import { useEffect, useRef, useState } from "react";
import Modal from "@/components/Modal";
import { copyText } from "@/lib/clipboard";
import { api, envPath, formatBytes, type Bucket } from "@/lib/platformClient";

type FileView = { key: string; name: string; size: number; lastModified: string };
type Listing = { prefix: string; folders: string[]; files: FileView[] };
type Upload = { name: string; progress: number; error?: string; done?: boolean };

// O limite de um envio pelo portal -- o mesmo do launcher.
const MAX_UPLOAD = 1 << 30;

// FileBrowser: os ficheiros de um bucket -- pastas, envio (vários, ou a
// arrastar), download, links e remoção. Tudo passa pelo portal e pelo
// launcher: as credenciais do storage nunca chegam aqui. A pasta aberta é
// de quem o usa (a página põe-na no URL).
export default function FileBrowser({
  project,
  env,
  addon,
  bucket,
  prefix,
  onPrefix: setPrefix,
}: {
  project: string;
  env: string;
  addon: string;
  bucket: Bucket;
  prefix: string;
  onPrefix: (prefix: string) => void;
}) {
  const [listing, setListing] = useState<Listing | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [uploads, setUploads] = useState<Upload[]>([]);
  const [folder, setFolder] = useState("");
  const [newFolder, setNewFolder] = useState(false);
  const [link, setLink] = useState<{ what: string; url: string } | null>(null);
  const [dragging, setDragging] = useState(false);
  const input = useRef<HTMLInputElement>(null);
  const base = envPath(project, env, "addons", addon, "buckets", bucket.name);
  const fileURL = (key: string) =>
    ["/api/files", project, env, addon, bucket.name, ...key.split("/")].map((p, i) => (i === 0 ? p : encodeURIComponent(p))).join("/");

  async function load(p = prefix) {
    try {
      setListing(await api<Listing>(`${base}/files?prefix=${encodeURIComponent(p)}`));
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    setListing(null);
    load(prefix);
  }, [base, prefix]);

  function send(file: File, key: string, index: number): Promise<void> {
    return new Promise((resolve) => {
      const update = (u: Partial<Upload>) => setUploads((all) => all.map((x, i) => (i === index ? { ...x, ...u } : x)));
      const xhr = new XMLHttpRequest();
      xhr.open("PUT", fileURL(key));
      xhr.setRequestHeader("Content-Type", file.type || "application/octet-stream");
      xhr.upload.onprogress = (e) => e.lengthComputable && update({ progress: e.loaded / e.total });
      xhr.onload = () => {
        if (xhr.status < 300) {
          update({ progress: 1, done: true });
        } else {
          let msg = `erro ${xhr.status}`;
          try {
            msg = JSON.parse(xhr.responseText).message ?? msg;
          } catch {}
          update({ error: msg });
        }
        resolve();
      };
      xhr.onerror = () => {
        update({ error: "a ligação caiu" });
        resolve();
      };
      xhr.send(file);
    });
  }

  async function uploadAll(files: FileList | File[]) {
    const list = Array.from(files);
    if (list.length === 0) return;
    setNotice(null);
    // Um pedido normal antes: renova a sessão (o envio não passa pelo
    // middleware que o faz).
    await load();
    const start = uploads.length;
    setUploads((all) => [...all, ...list.map((f) => ({ name: f.name, progress: 0, error: f.size > MAX_UPLOAD ? "mais de 1 GB -- usa a CLI (bs port-forward)" : undefined }))]);
    for (const [i, f] of list.entries()) {
      if (f.size > MAX_UPLOAD) continue;
      await send(f, prefix + f.name, start + i);
    }
    await load();
  }

  async function copyLink(f: FileView) {
    setError(null);
    let url: string;
    try {
      url = (await api<{ url: string }>(`${base}/link?key=${encodeURIComponent(f.key)}`)).url;
    } catch (e) {
      setError((e as Error).message);
      return;
    }
    const what = bucket.public ? `Link de ${f.name}` : `Link de ${f.name} (assinado, válido 1 hora)`;
    try {
      await copyText(url, `${what} copiado.`);
      setLink(null);
    } catch {
      // O browser não deixou copiar: fica à vista para copiar à mão.
      setLink({ what, url });
    }
  }

  async function remove(f: FileView) {
    if (!confirm(`Apagar ${f.key}? Não há como o recuperar.`)) return;
    try {
      await api(`${base}/files/${f.key.split("/").map(encodeURIComponent).join("/")}`, { method: "DELETE" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  function openFolder() {
    const name = folder.trim().replace(/^\/+|\/+$/g, "");
    if (!name) return;
    setPrefix(prefix + name + "/");
    setFolder("");
    setNewFolder(false);
    setNotice(`Pasta ${name}: só passa a existir quando enviares um ficheiro para lá.`);
  }

  const parts = prefix.split("/").filter(Boolean);

  return (
    <div
      className="card full"
      onDragOver={(e) => {
        e.preventDefault();
        setDragging(true);
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        uploadAll(e.dataTransfer.files);
      }}
      style={dragging ? { outline: "2px dashed var(--accent, #4f8cff)", outlineOffset: 4 } : undefined}
    >
      <p className="muted">
        <button className="link" onClick={() => setPrefix("")}>
          {bucket.name}
        </button>
        {parts.map((p, i) => (
          <span key={i}>
            {" / "}
            <button className="link" onClick={() => setPrefix(parts.slice(0, i + 1).join("/") + "/")}>
              {p}
            </button>
          </span>
        ))}
      </p>
      {error && <div className="error">{error}</div>}
      {notice && <div className="notice">{notice}</div>}
      {link && (
        <div className="notice">
          {link.what} -- copia-o daqui:
          <input readOnly value={link.url} onFocus={(e) => e.target.select()} style={{ width: "100%" }} />
          <button className="secondary" onClick={() => setLink(null)}>
            Fechar
          </button>
        </div>
      )}

      <div className="row">
        <input ref={input} type="file" multiple hidden onChange={(e) => e.target.files && uploadAll(e.target.files).then(() => (e.target.value = ""))} />
        <button className="primary" onClick={() => input.current?.click()}>
          Enviar ficheiros
        </button>
        <button className="secondary" onClick={() => setNewFolder(true)}>
          Nova pasta
        </button>
        <span className="muted">ou arrasta ficheiros para aqui (até 1 GB cada)</span>
      </div>

      {newFolder && (
        <Modal title="Nova pasta" onClose={() => setNewFolder(false)}>
          <p className="muted">
            Dentro de <code>{bucket.name}/{prefix}</code>. Uma pasta só passa a existir quando lá enviares um ficheiro.
          </p>
          <label htmlFor="folder-name">Nome</label>
          <input id="folder-name" value={folder} onChange={(e) => setFolder(e.target.value)} placeholder="fotos" onKeyDown={(e) => e.key === "Enter" && openFolder()} />
          <button className="primary" disabled={!folder.trim()} onClick={openFolder}>
            Abrir pasta
          </button>
        </Modal>
      )}

      {uploads.length > 0 && (
        <ul className="plain">
          {uploads.map((u, i) => (
            <li key={i}>
              {u.name}{" "}
              {u.error ? (
                <span className="error-text">{u.error}</span>
              ) : u.done ? (
                <span className="muted">enviado</span>
              ) : (
                <progress value={u.progress} max={1} />
              )}
            </li>
          ))}
          {uploads.every((u) => u.done || u.error) && (
            <li>
              <button className="link" onClick={() => setUploads([])}>
                limpar
              </button>
            </li>
          )}
        </ul>
      )}

      {!listing ? (
        <p className="muted">A carregar...</p>
      ) : listing.folders.length === 0 && listing.files.length === 0 ? (
        <p className="muted">Pasta vazia.</p>
      ) : (
        <div style={{ overflowX: "auto" }}>
          <table>
            <thead>
              <tr>
                <th>Nome</th>
                <th>Tamanho</th>
                <th>Alterado</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {prefix && (
                <tr>
                  <td colSpan={4}>
                    <button className="link" onClick={() => setPrefix(parts.slice(0, -1).map((p) => p + "/").join(""))}>
                      ↑ voltar
                    </button>
                  </td>
                </tr>
              )}
              {listing.folders.map((d) => (
                <tr key={"d:" + d}>
                  <td colSpan={4}>
                    <button className="link" onClick={() => setPrefix(prefix + d + "/")}>
                      📁 {d}
                    </button>
                  </td>
                </tr>
              ))}
              {listing.files.map((f) => (
                <tr key={f.key}>
                  <td>{f.name}</td>
                  <td>{formatBytes(f.size)}</td>
                  <td>{new Date(f.lastModified).toLocaleString("pt-PT")}</td>
                  <td>
                    <div className="actions">
                      <a className="button secondary" href={fileURL(f.key)} download={f.name}>
                        Descarregar
                      </a>
                      <button className="secondary" onClick={() => copyLink(f)}>
                        Copiar link
                      </button>
                      <button className="danger" onClick={() => remove(f)}>
                        Apagar
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
