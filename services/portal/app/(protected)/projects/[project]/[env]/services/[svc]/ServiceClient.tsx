"use client";

import Loading from "@/components/Skeleton";
import Breadcrumbs from "@/components/Breadcrumbs";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import {
  api,
  joinArgs,
  servicePath,
  splitArgs,
  statusBadge,
  type Deployment,
  type Replica,
  type Revision,
  type Service,
} from "@/lib/platformClient";
import TabBar, { useActiveTab } from "@/components/TabBar";
import TerminalModal from "@/components/TerminalModal";
import ConfirmModal from "../../../../../admin/database/ConfirmModal";
import BasicAuthCard from "./BasicAuthCard";
import DomainsCard from "./DomainsCard";
import ServiceConfig from "./ServiceConfig";
import TemplateCard from "./TemplateCard";
import { useCan } from "@/lib/projectRole";

const TABS = [
  { key: "overview", label: "Visão geral" },
  { key: "deploys", label: "Deploys e revisões" },
  { key: "domains", label: "Domínios" },
  { key: "config", label: "Configuração" },
] as const;

export default function ServiceClient({ project, env, svc }: { project: string; env: string; svc: string }) {
  const canEdit = useCan("developer");
  const router = useRouter();
  const tab = useActiveTab(TABS);
  const [service, setService] = useState<Service | null>(null);
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [revisions, setRevisions] = useState<Revision[]>([]);
  const [replicas, setReplicas] = useState<Replica[]>([]);
  const [open, setOpen] = useState<Deployment | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  // O deploy lançado desta página, acompanhado até terminar -- o resultado
  // fica à vista até ser fechado (as réplicas antigas continuam verdes
  // quando um deploy falha, por isso o selo não chega).
  const [tracking, setTracking] = useState<string | null>(null);
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [image, setImage] = useState("");
  const [command, setCommand] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [terminal, setTerminal] = useState(false);
  const path = (...rest: string[]) => servicePath(project, env, svc, ...rest);

  async function load() {
    try {
      const [s, d, r, rep] = await Promise.all([
        api<Service>(path()),
        api<Deployment[]>(path("deployments")),
        api<Revision[]>(path("revisions")),
        api<Replica[]>(path("replicas")),
      ]);
      setService(s);
      setDeployments(d);
      setRevisions(r);
      setReplicas(rep);
      setError(null);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function showDeployment(id: string) {
    try {
      setOpen(await api<Deployment>(path("deployments", id)));
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function checkTracked(id: string) {
    try {
      const d = await api<Deployment>(path("deployments", id));
      if (!d.finishedAt) return;
      setTracking(null);
      setNotice(null);
      setResult(
        d.status === "succeeded"
          ? { ok: true, text: `Deploy da revisão #${d.revisionNumber} concluído.` }
          : {
              ok: false,
              text: `O deploy da revisão #${d.revisionNumber} falhou: ${d.error || d.status}. As réplicas anteriores continuam a servir.`,
            },
      );
    } catch {
      // Tenta outra vez no próximo ciclo.
    }
  }

  useEffect(() => {
    load();
    const t = setInterval(() => {
      load();
      if (open && !open.finishedAt) showDeployment(open.id);
      if (tracking) checkTracked(tracking);
    }, 3000);
    return () => clearInterval(t);
  }, [project, env, svc, open?.id, open?.finishedAt, tracking]);

  async function deploy(body: object, what: string) {
    setError(null);
    setNotice(null);
    try {
      const d = await api<Deployment>(path("deployments"), { method: "POST", body: JSON.stringify(body) });
      setNotice(`${what} iniciado.`);
      setResult(null);
      setTracking(d.id);
      setImage("");
      setOpen(d);
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function setState(state: "start" | "stop") {
    try {
      await api(path(state), { method: "POST" });
      await load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function runCommand() {
    setError(null);
    try {
      await api(path("run"), { method: "POST", body: JSON.stringify({ cmd: splitArgs(command) }) });
      setNotice("Comando a correr -- o resultado aparece no separador Jobs do ambiente.");
      setCommand("");
    } catch (e) {
      setError((e as Error).message);
    }
  }

  async function remove() {
    setConfirmDelete(false);
    try {
      await api(path(), { method: "DELETE" });
      router.push(`/projects/${project}/${env}`);
    } catch (e) {
      setError((e as Error).message);
    }
  }

  if (!service) {
    return (
      <div className="card full">
        <h1>{svc}</h1>
        {error ? <div className="error">{error}</div> : <Loading />}
      </div>
    );
  }

  const current = service.currentRevision;

  return (
    <>
      <div className="card full">
        <Breadcrumbs
          items={[
            { label: "Projetos", href: "/projects" },
            { label: project, href: `/projects/${project}` },
            { label: env, href: `/projects/${project}/${env}` },
            { label: svc },
          ]}
        />
        <h1>
          {svc}{" "}
          {service.deploying ? (
            <span className={statusBadge("deploying")}>a fazer deploy</span>
          ) : service.desiredState === "stopped" ? (
            <span className="badge">parado</span>
          ) : (
            <span className={statusBadge(service.readyReplicas > 0 ? "running" : "failed")}>
              {service.readyReplicas} réplica(s) pronta(s)
            </span>
          )}
        </h1>
        {error && <div className="error">{error}</div>}
        {notice && <div className="notice">{notice}</div>}
        {result && (
          <div className={result.ok ? "notice" : "error"} style={{ display: "flex", justifyContent: "space-between", gap: 12 }}>
            <span>{result.text}</span>
            <button className="secondary" onClick={() => setResult(null)}>
              Fechar
            </button>
          </div>
        )}
        {service.problem && <div className="error">{service.problem}</div>}
        <p>
          {service.kind === "web" ? `Web na porta ${service.port}` : "Worker"} ·{" "}
          {current ? (
            <>
              revisão <strong>#{current.number}</strong> <code>{current.image}</code>
            </>
          ) : (
            "ainda sem deploy"
          )}
        </p>
        {service.urls.map((u) => (
          <p key={u}>
            <a href={u} target="_blank" rel="noreferrer">
              {u}
            </a>
          </p>
        ))}

{canEdit && (
        <>
        <h2>Deploy</h2>
        <div className="row">
          <input
            value={image}
            onChange={(e) => setImage(e.target.value)}
            placeholder={current ? current.image : "ghcr.io/eu/app:1.0.0"}
          />
        </div>
        <div className="actions">
          <button className="secondary" disabled={!image.trim() || service.deploying} onClick={() => deploy({ image: image.trim() }, "Deploy")}>
            Fazer deploy
          </button>
          <button
            className="secondary"
            disabled={(!current && revisions.length === 0) || service.deploying}
            onClick={() => deploy({}, current ? "Redeploy" : "Nova tentativa")}
            title={
              current
                ? `Mesma imagem (${current.image}), com a configuração atual (env, recursos, comandos)`
                : revisions.length > 0
                  ? `Nenhum deploy chegou a correr: tenta outra vez ${revisions[0].image} com a configuração atual`
                  : "Ainda não houve nenhum deploy -- indica uma imagem"
            }
          >
            {current || revisions.length === 0 ? "Redeploy com a configuração atual" : `Tentar de novo (${revisions[0].image})`}
          </button>
          {service.desiredState === "running" ? (
            <button className="secondary" onClick={() => setState("stop")}>
              Parar
            </button>
          ) : (
            <button className="secondary" onClick={() => setState("start")}>
              Ligar
            </button>
          )}
          <button className="danger" onClick={() => setConfirmDelete(true)}>
            Apagar serviço
          </button>
        </div>
        </>
        )}

      </div>

      <TemplateCard project={project} env={env} service={service} onChanged={load} />

      <TabBar tabs={TABS} active={tab} />

      {tab === "overview" && (
      <div className="card full">
        <div className="row" style={{ justifyContent: "space-between" }}>
          <h2>Réplicas</h2>
          {canEdit && (
            <button
              className="secondary"
              disabled={!replicas.some((r) => r.state === "running")}
              onClick={() => setTerminal(true)}
              title="Shell dentro de uma réplica (com o env do serviço, incluindo segredos)"
            >
              Terminal
            </button>
          )}
        </div>
        {replicas.length === 0 ? (
          <p className="muted">Nenhuma réplica a correr.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Container</th>
                <th>Revisão</th>
                <th>Nó</th>
                <th>Estado</th>
                <th>Tráfego</th>
              </tr>
            </thead>
            <tbody>
              {replicas.map((r) => (
                <tr key={r.name}>
                  <td>
                    <code>{r.name}</code>
                  </td>
                  <td>#{r.revisionNumber}</td>
                  <td>{r.node}</td>
                  <td>{r.status}</td>
                  <td>
                    {r.draining ? (
                      <span className={statusBadge("pending")}>a drenar</span>
                    ) : r.ready ? (
                      <span className={statusBadge("ready")}>pronta</span>
                    ) : (
                      <span className={statusBadge("failed")}>fora</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}

{canEdit && (
        <>
        <h2>Correr comando</h2>
        <p className="muted">Corre uma vez com a imagem e o env da revisão atual (ex: php artisan cache:clear).</p>
        <div className="row">
          <input value={command} onChange={(e) => setCommand(e.target.value)} placeholder="php artisan cache:clear" />
          <button className="secondary" disabled={!command.trim() || !current} onClick={runCommand}>
            Correr
          </button>
        </div>
        </>
        )}
      </div>
      )}

      {tab === "deploys" && (
      <div className="card full">
        <h2>Deploys</h2>
        {deployments.length === 0 ? (
          <p className="muted">Nenhum deploy ainda.</p>
        ) : (
          <table>
            <thead>
              <tr>
                <th>Revisão</th>
                <th>Estado</th>
                <th>Imagem</th>
                <th>Por</th>
                <th>Quando</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {deployments.map((d) => (
                <tr key={d.id}>
                  <td>#{d.revisionNumber}</td>
                  <td>
                    <span className={statusBadge(d.status)}>{d.status}</span>
                  </td>
                  <td>
                    <code className="truncate">{d.image}</code>
                  </td>
                  <td>{d.triggeredBy}</td>
                  <td>{new Date(d.createdAt).toLocaleString("pt-PT")}</td>
                  <td>
                    <button className="secondary" onClick={() => showDeployment(d.id)}>
                      Log
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {open && (
          <>
            <h2>
              Deploy da revisão #{open.revisionNumber} · <span className={statusBadge(open.status)}>{open.status}</span>
            </h2>
            {open.error && <div className="error">{open.error}</div>}
            <pre className="generated">{open.log || "(a começar...)"}</pre>
            <button className="secondary" onClick={() => setOpen(null)}>
              Fechar
            </button>
          </>
        )}

        <h2>Revisões</h2>
        <table>
          <thead>
            <tr>
              <th>#</th>
              <th>Imagem</th>
              <th>Criada</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {revisions.map((r) => (
              <tr key={r.id}>
                <td>
                  #{r.number} {current?.id === r.id && <span className="badge active">atual</span>}
                </td>
                <td>
                  <code className="truncate">{r.image}</code>
                </td>
                <td>
                  {new Date(r.createdAt).toLocaleString("pt-PT")} · {r.createdBy}
                </td>
                <td>
                  {current?.id !== r.id && (
                    <button
                      className="secondary"
                      disabled={service.deploying}
                      onClick={() => deploy({ revision: r.number }, `Rollback para #${r.number}`)}
                    >
                      Voltar a esta
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>

      </div>
      )}

      {tab === "domains" &&
        (service.kind === "web" ? (
          <>
            <DomainsCard base={servicePath(project, env, svc, "domains")} />
            <BasicAuthCard project={project} env={env} service={service} onSaved={load} />
          </>
        ) : (
          <div className="card full">
            <p className="muted">Só serviços web têm domínios.</p>
          </div>
        ))}

      {tab === "config" && <ServiceConfig project={project} env={env} service={service} onSaved={load} joinArgs={joinArgs} />}

      {confirmDelete && (
        <ConfirmModal
          title="Apagar serviço"
          message={`Remove todas as réplicas de ${svc} e o seu histórico de deploys.`}
          expectedText={svc}
          confirmLabel="Apagar"
          onConfirm={remove}
          onCancel={() => setConfirmDelete(false)}
        />
      )}
      {terminal && (
        <TerminalModal
          title={`Terminal · ${svc}`}
          path={path("terminal")}
          replicas={replicas.filter((r) => r.state === "running").map((r) => r.name)}
          onClose={() => setTerminal(false)}
        />
      )}
    </>
  );
}
