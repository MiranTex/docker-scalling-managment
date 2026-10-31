"use client";

import Loading from "@/components/Skeleton";
import Breadcrumbs from "@/components/Breadcrumbs";
import TabBar, { useActiveTab } from "@/components/TabBar";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";
import { api, envPath, type Addon, type Bucket } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";
import DomainsCard from "../../../../services/[svc]/DomainsCard";
import FileBrowser from "../../../../FileBrowser";
import { parseOrigins } from "../../AddonClient";

const TABS = [
  { key: "files", label: "Ficheiros" },
  { key: "access", label: "Acesso" },
  { key: "domains", label: "Domínios" },
] as const;

// BucketClient: um bucket de um storage -- os ficheiros (a pasta aberta
// vive no URL, ?pasta=), quem os lê e os domínios próprios.
export default function BucketClient({ project, env, addon: slug, bucket: name }: { project: string; env: string; addon: string; bucket: string }) {
  const canEdit = useCan("developer");
  const router = useRouter();
  const params = useSearchParams();
  const tab = useActiveTab(TABS);
  const prefix = params.get("pasta") ?? "";
  const [addon, setAddon] = useState<Addon | null>(null);
  const [bucket, setBucket] = useState<Bucket | null>(null);
  const [cors, setCors] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const base = envPath(project, env, "addons", slug);
  const envHref = `/projects/${project}/${env}`;
  const addonHref = `${envHref}/addons/${slug}`;

  async function load() {
    try {
      const [all, buckets] = await Promise.all([api<Addon[]>(envPath(project, env, "addons")), api<Bucket[]>(`${base}/buckets`)]);
      setAddon(all.find((a) => a.slug === slug) ?? null);
      const b = buckets.find((x) => x.name === name);
      if (!b) {
        setError(`O bucket ${name} não existe.`);
        return;
      }
      setBucket(b);
      setCors(b.corsOrigins.join(", "));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
  }, [base, name]);

  async function update(body: object, done: string) {
    setError(null);
    setNotice(null);
    try {
      await api(`${base}/buckets/${encodeURIComponent(name)}`, { method: "PATCH", body: JSON.stringify(body) });
      setNotice(done);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  function setPublic(value: boolean) {
    if (value && !confirm(`Tornar ${name} público? Qualquer pessoa com o endereço de um ficheiro passa a poder lê-lo (listar o bucket continua fechado).`)) return;
    update({ public: value }, value ? `${name} é agora público.` : `${name} é agora privado.`);
  }

  async function remove() {
    if (!confirm(`Apagar o bucket ${name}? Só é possível se estiver vazio.`)) return;
    try {
      await api(`${base}/buckets/${encodeURIComponent(name)}`, { method: "DELETE" });
      router.push(`${addonHref}?tab=buckets`);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  const openFolder = (p: string) => router.replace(p ? `?pasta=${encodeURIComponent(p)}` : "?", { scroll: false });

  if (!bucket || !addon) {
    return error ? <div className="error">{error}</div> : <Loading />;
  }
  const activeDomain = bucket.domains.find((d) => d.status === "active");

  return (
    <>
      <div className="card full">
        <Breadcrumbs
          items={[
            { label: "Projetos", href: "/projects" },
            { label: project, href: `/projects/${project}` },
            { label: env, href: `${envHref}?tab=addons` },
            { label: slug, href: `${addonHref}?tab=buckets` },
            { label: name },
          ]}
        />
        <h1>
          {name} {bucket.public ? <span className="badge active">pública (CDN)</span> : <span className="badge">privada</span>}
        </h1>
        {error && <div className="error">{error}</div>}
        {notice && <div className="notice">{notice}</div>}
      </div>

      <TabBar tabs={TABS} active={tab} />

      {tab === "files" &&
        (canEdit ? (
          <FileBrowser project={project} env={env} addon={slug} bucket={bucket} prefix={prefix} onPrefix={openFolder} />
        ) : (
          <div className="card full">
            <p className="muted">Os ficheiros só se veem com o papel developer -- podem ter dados dos clientes.</p>
          </div>
        ))}

      {tab === "access" && (
        <>
          <div className="card full">
            <h2>Leitura</h2>
            <select value={bucket.public ? "public" : "private"} disabled={!canEdit} onChange={(e) => setPublic(e.target.value === "public")}>
              <option value="private">privada -- abre-se com links assinados</option>
              <option value="public">pública -- qualquer pessoa com o endereço (CDN)</option>
            </select>
            <p className="muted">Escrever exige sempre as credenciais, e listar o conteúdo nunca é público.</p>
            <h2>Endereços</h2>
            {bucket.public ? (
              <ul className="plain">
                {activeDomain && (
                  <li>
                    <code>
                      https://{activeDomain.hostname}/&lt;ficheiro&gt;
                    </code>
                  </li>
                )}
                {bucket.url ? (
                  <li>
                    <code>{bucket.url}/&lt;ficheiro&gt;</code>
                  </li>
                ) : (
                  !activeDomain && <li className="muted">Liga o endereço público do {slug} (ou acrescenta um domínio) para o bucket ser lido de fora.</li>
                )}
              </ul>
            ) : bucket.url ? (
              <p className="muted">
                Os ficheiros abrem-se com links assinados, gerados pela app ou em <strong>Ficheiros → Copiar link</strong> (válidos 1
                hora), em <code>{bucket.url}/…</code>.
              </p>
            ) : (
              <p className="muted">Sem o endereço público do {slug}, os ficheiros só são acessíveis dentro do ambiente.</p>
            )}
          </div>

          <div className="card full">
            <h2>CORS</h2>
            <p className="muted">
              As origens de onde um browser pode ler e enviar ficheiros diretamente (ex: uploads com links assinados). Uma por
              linha ou separadas por vírgulas; <code>*</code> para todas.
            </p>
            <div className="row">
              <input style={{ minWidth: 320 }} value={cors} disabled={!canEdit} onChange={(e) => setCors(e.target.value)} placeholder="https://app.loja.pt" />
              {canEdit && cors !== bucket.corsOrigins.join(", ") && (
                <button className="secondary" onClick={() => update({ corsOrigins: parseOrigins(cors) }, "CORS guardado.")}>
                  Guardar
                </button>
              )}
            </div>
          </div>

          {canEdit && name !== "app" && (
            <div className="card full">
              <h2>Zona de perigo</h2>
              <p className="muted">Um bucket só se apaga vazio -- apaga primeiro os ficheiros.</p>
              <button className="danger" onClick={remove}>
                Apagar bucket
              </button>
            </div>
          )}
        </>
      )}

      {tab === "domains" &&
        (bucket.public ? (
          <DomainsCard
            base={`${base}/buckets/${encodeURIComponent(name)}/domains`}
            title={`Domínios de ${name}`}
            owner={`o bucket ${name}`}
            placeholder="cdn.loja.pt"
          />
        ) : (
          <div className="card full">
            <p className="muted">
              Só um bucket público tem domínio próprio (ex: <code>cdn.loja.pt/foto.jpg</code> serve <code>{name}/foto.jpg</code>).
              Torna-o público em <strong>Acesso</strong>.
            </p>
          </div>
        ))}
    </>
  );
}
