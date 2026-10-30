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

export type ProjectDetail = Project & {
  environments: Environment[];
  apiUrl?: string;
  // O que quem pede pode fazer neste projeto (ver lib/projectRole.tsx).
  myRole?: "platform-admin" | "admin" | "developer" | "viewer";
};

export type Member = { userId: string; email: string; role: "viewer" | "developer" | "admin"; addedAt: string; addedBy: string };

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
  status: "running" | "succeeded" | "failed" | "cancelled";
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
  if (status === "cancelled") return "badge";
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
  problem?: string;
  metricsPort: number;
  metricsPath: string;
  template?: TemplateRef;
  createdAt: string;
  updatedAt: string;
  // Com utilizador, o serviço está protegido com password (basic auth).
  basicAuthUser?: string;
  // As etiquetas que um nó tem de ter para receber réplicas.
  nodeSelector?: Record<string, string>;
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
  node: string;
};

export type NodeView = {
  name: string;
  address: string;
  os: string;
  arch: string;
  cpus: number;
  memoryMb: number;
  dockerVersion: string;
  schedulable: boolean;
  lastSeenAt: string;
  online: boolean;
  allocated: Usage;
  replicas: number;
  addons: number;
  capacityMemoryMb: number;
  kind: "local" | "agent";
  agentVersion?: string;
  // "local", "cluster" (recebe apps) ou o motivo por que ainda não tem rede.
  network: string;
  // "online", "sem sinal há Ns" ou "perdido".
  status: string;
  draining: boolean;
  drain?: string;
  agentOutdated?: boolean;
  agentLatest?: string;
  // As etiquetas que um admin pôs e as detetadas (ex: gpu=nvidia).
  labels: Record<string, string>;
  autoLabels: Record<string, string>;
};

export type NodeJoinToken = { token: string; expiresAt: string; caHash: string; command: string };
export type NodeCheck = { ok: boolean; output: string; error?: string };

export type ImageTag = { tag: string; digest: string; platforms: string[]; size: number; created?: string };
export type PlatformImage = { repository: string; reference: string; tags: ImageTag[] };
export type ProjectImages = { enabled: boolean; host?: string; images: PlatformImage[] };

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

export type DeployToken = {
  id: string;
  name: string;
  prefix: string;
  environmentId?: string;
  createdAt: string;
  createdBy: string;
  lastUsedAt?: string;
  revokedAt?: string;
};

export type Domain = {
  hostname: string;
  status: "pending" | "active" | "failed";
  error?: string;
  checkedAt?: string;
  verifiedAt?: string;
  createdAt: string;
  expectedIps: string[];
  expectedCname?: string;
};

export type VolumeView = {
  name: string;
  createdAt: string;
  sizeBytes: number;
  status: "in-use" | "free" | "orphan";
  project: string;
  environment?: string;
  addon?: string;
  kind?: "data" | "backups";
  usedBy: { container: string; state: string; path: string }[];
  deletable: boolean;
  reason?: string;
};

export type NetworkMember = {
  container: string;
  kind: "service" | "addon" | "job" | "platform" | "other";
  name?: string;
  state?: string;
};

export type EnvironmentNetwork = { name: string; subnet?: string; gateway?: string; members: NetworkMember[] };

export type TemplateSpec = {
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
  metricsPort: number;
  metricsPath: string;
};

export type Template = {
  id: string;
  slug: string;
  name: string;
  description: string;
  latestVersion: number;
  usedBy: number;
  archivedAt?: string;
  createdAt: string;
  createdBy: string;
};

export type TemplateVersion = {
  id: string;
  version: number;
  image: string;
  notes: string;
  spec: TemplateSpec;
  createdAt: string;
  createdBy: string;
};

export type TemplateDetail = Template & { versions: TemplateVersion[] };

export type TemplateRef = { slug: string; name: string; version: number; latestVersion: number; archived: boolean };

export type FieldChange = { field: string; from: unknown; to: unknown; current: unknown; overrides: boolean };

export type TemplateUpdate = {
  template: string;
  fromVersion: number;
  toVersion: number;
  changes: FieldChange[];
  notes: { version: number; notes: string }[];
};

// Nome de cada campo da spec como aparece no formulário do serviço.
export const SPEC_LABELS: Record<string, string> = {
  kind: "Tipo",
  port: "Porta",
  healthPath: "Health check",
  healthTimeoutSeconds: "Tempo para ficar saudável (s)",
  vcpu: "vCPU por réplica",
  memoryMb: "Memória por réplica (MB)",
  metricsPort: "Porta de métricas",
  metricsPath: "Caminho de métricas",
  cmd: "Comando",
  releaseCmd: "Release / migrations",
  env: "Variáveis de ambiente",
  scalingMode: "Modo de scaling",
  replicas: "Réplicas",
  minReplicas: "Mínimo de réplicas",
  maxReplicas: "Máximo de réplicas",
  cpuScaleUpPercent: "Sobe acima de (% CPU)",
  cpuScaleDownPercent: "Desce abaixo de (% CPU)",
};

// formatSpecValue mostra um valor da spec de forma legível na tabela de
// diferenças (listas uma por linha, vazio como "—").
export function formatSpecValue(v: unknown): string {
  if (Array.isArray(v)) return v.length ? v.join("\n") : "—";
  if (v === "" || v === null || v === undefined || v === 0) return "—";
  return String(v);
}
