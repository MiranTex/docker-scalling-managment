// Tipos da API de plataforma (projetos, ambientes, serviços...) servida
// pelo launcher em /v1/projects -- ver services/launcher/internal/platformapi.
// O browser nunca fala com o launcher diretamente: passa sempre pelo proxy
// em app/api/platform/[...path], que acrescenta o access token da sessão.

export type Project = {
  id: string;
  slug: string;
  name: string;
  quotaVcpu: number;
  quotaMemoryMb: number;
  createdAt: string;
  createdBy: string;
};

export type Environment = {
  id: string;
  projectId: string;
  slug: string;
  network: string;
  createdAt: string;
  createdBy: string;
};

export type ProjectDetail = Project & { environments: Environment[] };

export type EnvSecret = {
  name: string;
  managedBy?: string;
  updatedAt: string;
  updatedBy: string;
};

export type ApiError = { error?: string; message?: string };

// api chama o proxy e devolve o JSON, ou lança Error com a mensagem do
// launcher -- é o formato {error, message} de toda a API.
export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/platform/${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers ?? {}) },
  });
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new Error((data as ApiError).message ?? `erro ${res.status}`);
  }
  return data as T;
}

export function projectPath(project: string, ...rest: string[]): string {
  return ["projects", project, ...rest].map(encodeURIComponent).join("/");
}

export function envPath(project: string, env: string, ...rest: string[]): string {
  return projectPath(project, "environments", env, ...rest);
}

export type Addon = {
  id: string;
  slug: string;
  kind: "postgres" | "redis";
  image: string;
  envVar: string;
  vcpu: number;
  memoryMb: number;
  backupEnabled: boolean;
  lastBackupAt?: string;
  createdAt: string;
  createdBy: string;
  status: "running" | "starting" | "unhealthy" | "stopped" | "missing";
  host: string;
};

export type Backup = { file: string; size: number };

export type Job = {
  id: string;
  kind: string;
  image: string;
  cmd: string[];
  status: "running" | "succeeded" | "failed";
  exitCode?: number;
  output: string;
  addonId?: string;
  serviceId?: string;
  revisionId?: string;
  createdAt: string;
  finishedAt?: string;
  createdBy: string;
};

export type Usage = { vcpu: number; memoryMb: number; containers: number };

// statusBadge escolhe a classe CSS de um estado (running/succeeded = verde,
// a meio = amarelo, o resto = vermelho).
export function statusBadge(status: string): string {
  if (["running", "succeeded", "healthy", "active", "ready"].includes(status)) return "badge active";
  if (["starting", "pending", "pulling", "releasing", "deploying", "switching"].includes(status)) return "badge pending";
  return "badge failed";
}

export function formatBytes(n: number): string {
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  if (n >= 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${n} B`;
}

export type Revision = {
  id: string;
  number: number;
  image: string;
  cmd: string[];
  env: string[];
  releaseCmd: string[];
  port: number;
  healthPath: string;
  healthTimeoutSeconds: number;
  vcpu: number;
  memoryMb: number;
  createdAt: string;
  createdBy: string;
};

export type Service = {
  id: string;
  slug: string;
  kind: "web" | "worker";
  port: number;
  healthPath: string;
  healthTimeoutSeconds: number;
  cmd: string[];
  env: string[];
  releaseCmd: string[];
  vcpu: number;
  memoryMb: number;
  scalingMode: "fixed" | "auto";
  replicas: number;
  minReplicas: number;
  maxReplicas: number;
  cpuScaleUpPercent: number;
  cpuScaleDownPercent: number;
  desiredState: "running" | "stopped";
  currentRevision?: Revision;
  readyReplicas: number;
  deploying: boolean;
  urls: string[];
  createdAt: string;
  updatedAt: string;
};

export type Deployment = {
  id: string;
  revisionId: string;
  revisionNumber: number;
  image: string;
  status: string;
  error?: string;
  log: string;
  triggeredBy: string;
  createdAt: string;
  finishedAt?: string;
};

export type Replica = {
  name: string;
  state: string;
  status: string;
  revisionNumber: number;
  ready: boolean;
  draining: boolean;
};

export type RegistryCredential = { registry: string; username: string; updatedAt: string; updatedBy: string };

export function servicePath(project: string, env: string, svc: string, ...rest: string[]): string {
  return envPath(project, env, "services", svc, ...rest);
}

// splitArgs parte uma linha de comando em argumentos, respeitando aspas
// simples e duplas -- "php artisan migrate --force" -> ["php", ...].
export function splitArgs(line: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote: string | null = null;
  let has = false;
  for (const ch of line.trim()) {
    if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
      has = true;
    } else if (/\s/.test(ch)) {
      if (has || cur) out.push(cur);
      cur = "";
      has = false;
    } else {
      cur += ch;
      has = true;
    }
  }
  if (has || cur) out.push(cur);
  return out;
}

export function joinArgs(args: string[]): string {
  return args.map((a) => (/[\s"']/.test(a) || a === "" ? JSON.stringify(a) : a)).join(" ");
}
