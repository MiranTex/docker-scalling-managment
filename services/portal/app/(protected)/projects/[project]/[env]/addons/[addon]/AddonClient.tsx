"use client";

import Loading from "@/components/Skeleton";
import Breadcrumbs from "@/components/Breadcrumbs";
import CopyCommand from "@/components/CopyCommand";
import TabBar, { useActiveTab } from "@/components/TabBar";
import TerminalModal from "@/components/TerminalModal";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { api, envPath, statusBadge, type Addon, type Bucket } from "@/lib/platformClient";
import { useCan } from "@/lib/projectRole";
import ConfirmModal from "../../../../../admin/database/ConfirmModal";

const TABS = [
  { key: "overview", label: "Visão geral" },
  { key: "buckets", label: "Buckets" },
] as const;

const EMPTY = { name: "", public: false, cors: "" };

export function parseOrigins(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((o) => o.trim())
    .filter(Boolean);
}

// AddonClient: a página de um addon storage -- por agora só os storages têm
// página própria (os outros geram-se na lista de addons do ambiente).
export default function AddonClient({ project, env, addon: slug }: { project: string; env: string; addon: string }) {
  const canEdit = useCan("developer");
  const router = useRouter();
  const tab = useActiveTab(TABS);
  const [addon, setAddon] = useState<Addon | null>(null);
  const [buckets, setBuckets] = useState<Bucket[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [form, setForm] = useState(EMPTY);
  const [terminal, setTerminal] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [purge, setPurge] = useState(false);
  const base = envPath(project, env, "addons", slug);
  const envHref = `/projects/${project}/${env}`;

  async function load() {
    try {
      const all = await api<Addon[]>(envPath(project, env, "addons"));
      const a = all.find((x) => x.slug === slug);
      if (!a) {
        setError(`O addon ${slug} não existe neste ambiente.`);
        return;
      }
      setAddon(a);
      if (a.kind === "storage") setBuckets(await api<Bucket[]>(`${base}/buckets`));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(load, 10000);
    return () => clearInterval(t);
  }, [base]);

  async function run(fn: () => Promise<unknown>, done?: string) {
    setError(null);
    setNotice(null);
    try {
      await fn();
      if (done) setNotice(done);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  if (!addon) {
    return error ? <div className="error">{error}</div> : <Loading />;
  }
  if (addon.kind !== "storage") {
    return (
      <div className="card full">
        <p>
          O {addon.slug} gere-se na <Link href={`${envHref}?tab=addons`}>lista de addons do ambiente</Link>.
        </p>
      </div>
    );
  }

  const togglePublic = () =>
    run(
      () => api(base, { method: "PATCH", body: JSON.stringify({ publicEndpoint: !addon.publicEndpoint }) }),
      addon.publicEndpoint
        ? `Endereço público desligado. Os serviços perdem ${addon.envVar}_PUBLIC_URL no próximo deploy.`
        : `Endereço público ligado. Os serviços recebem ${addon.envVar}_PUBLIC_URL no próximo deploy.`,
    );

  const create = () =>
    run(async () => {
      const name = form.name.trim();
      await api(`${base}/buckets`, {
        method: "POST",
        body: JSON.stringify({ name, public: form.public, corsOrigins: parseOrigins(form.cors) }),
      });
      setForm(EMPTY);
      router.push(`${envHref}/addons/${slug}/buckets/${name}`);
    });

  async function remove() {
    setConfirmDelete(false);
    try {
      await api(`${base}${purge ? "?purge=true" : ""}`, { method: "DELETE" });
      router.push(`${envHref}?tab=addons`);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  return (
    <>
      <div className="card full">
        <Breadcrumbs
          items={[
            { label: "Projetos", href: "/projects" },
            { label: project, href: `/projects/${project}` },
            { label: env, href: `${envHref}?tab=addons` },
            { label: slug },
          ]}
        />
        <h1>
          {slug} <span className={statusBadge(addon.status)}>{addon.status}</span>
        </h1>
        {error && <div className="error">{error}</div>}
        {notice && <div className="notice">{notice}</div>}
        <p className="muted">
          Armazenamento de ficheiros (API do S3) · {addon.image} · {addon.vcpu} vCPU / {addon.memoryMb} MB
        </p>
        {canEdit && (
          <div className="actions">
            <button className="secondary" onClick={() => setTerminal(true)} title="Shell no container (weed shell disponível)">
              Terminal
            </button>
            <button className="danger" onClick={() => setConfirmDelete(true)}>
              Apagar addon
            </button>
          </div>
        )}
      </div>

      <TabBar tabs={TABS} active={tab} />

      {tab === "overview" && (
        <>
          <div className="card full">
            <h2>Dentro do ambiente</h2>
            <p>
              Os serviços chegam-lhe por <code>http://{addon.host}</code> e recebem estas variáveis:
            </p>
            <ul className="plain">
              {addon.envVars.map((v) => (
                <li key={v}>
                  <code>{v}</code>
                </li>
              ))}
            </ul>
            {canEdit && (
              <>
                <p className="muted">Da tua máquina (aws cli, rclone, Cyberduck), com a CLI bs:</p>
                <CopyCommand command={`bs port-forward ${project}/${env}/${slug}`} />
              </>
            )}
          </div>

          <div className="card full">
            <h2>Endereço público</h2>
            <p className="muted">
              Sem ele, o storage só é acessível aos serviços deste ambiente. Ligado, fica em HTTPS: os buckets privados
              abrem-se com links assinados pela app, os públicos por qualquer pessoa. Escrever exige sempre as credenciais.
            </p>
            <label className="checkbox">
              <input type="checkbox" checked={!!addon.publicEndpoint} disabled={!canEdit} onChange={togglePublic} /> endereço
              público
            </label>
            {addon.publicUrl && (
              <p>
                <code>{addon.publicUrl}</code> -- nos serviços como <code>{addon.envVar}_PUBLIC_URL</code>
              </p>
            )}
          </div>
        </>
      )}

      {tab === "buckets" && (
        <div className="card full">
          {!buckets ? (
            <Loading />
          ) : (
            <div style={{ overflowX: "auto" }}>
              <table>
                <thead>
                  <tr>
                    <th>Bucket</th>
                    <th>Leitura</th>
                    <th>CORS</th>
                    <th>Domínios</th>
                  </tr>
                </thead>
                <tbody>
                  {buckets.map((b) => (
                    <tr key={b.name}>
                      <td>
                        <Link href={`${envHref}/addons/${slug}/buckets/${b.name}`}>
                          <strong>{b.name}</strong>
                        </Link>
                        {b.name === "app" && <div className="muted">o de {addon.envVar}_BUCKET</div>}
                      </td>
                      <td>{b.public ? <span className="badge active">pública (CDN)</span> : <span className="badge">privada</span>}</td>
                      <td>{b.corsOrigins.length ? b.corsOrigins.join(", ") : <span className="muted">—</span>}</td>
                      <td>{b.domains.length ? b.domains.map((d) => d.hostname).join(", ") : <span className="muted">—</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {canEdit && (
            <>
              <h2>Novo bucket</h2>
              <div className="form-grid">
                <div>
                  <label htmlFor="bucket-name">Nome</label>
                  <input id="bucket-name" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="media" />
                </div>
                <div>
                  <label htmlFor="bucket-cors">CORS (opcional)</label>
                  <input id="bucket-cors" value={form.cors} onChange={(e) => setForm({ ...form, cors: e.target.value })} placeholder="https://app.loja.pt" />
                </div>
              </div>
              <label className="checkbox">
                <input type="checkbox" checked={form.public} onChange={(e) => setForm({ ...form, public: e.target.checked })} /> leitura
                pública (CDN)
              </label>
              <button className="primary" disabled={!form.name.trim()} onClick={create}>
                Criar bucket
              </button>
            </>
          )}
        </div>
      )}

      {terminal && <TerminalModal title={`Terminal · ${slug}`} path={`${base}/terminal`} onClose={() => setTerminal(false)} />}

      {confirmDelete && (
        <ConfirmModal
          title="Apagar addon"
          message={`Remove o container de ${slug} e os segredos ${addon.envVar}_*. Os ficheiros ficam no volume, a não ser que marques "apagar dados".`}
          expectedText={slug}
          confirmLabel={purge ? "Apagar com dados" : "Apagar"}
          onConfirm={remove}
          onCancel={() => setConfirmDelete(false)}
        >
          <label className="checkbox">
            <input type="checkbox" checked={purge} onChange={(e) => setPurge(e.target.checked)} /> apagar dados
          </label>
        </ConfirmModal>
      )}
    </>
  );
}
