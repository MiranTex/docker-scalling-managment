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
