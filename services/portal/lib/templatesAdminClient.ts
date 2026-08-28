// Cliente fino para o templatesadmin (services/templatesadmin, API de
// administração de modelos de serviço) -- mesmo padrão de
// secretsAdminClient.ts: só chamado do lado do servidor (Route
// Handlers), TEMPLATESADMIN_SERVICE_URL é interno à rede Docker e nunca
// deve ser exposto ao browser.

export const TEMPLATESADMIN_SERVICE_URL = process.env.TEMPLATESADMIN_SERVICE_URL ?? "http://localhost:8093";

// Só o que o container da aplicação precisa -- sem nenhum campo de
// autoscaling (réplicas, thresholds de CPU, target service, portas de
// host). Essa config passou a ser preenchida no momento de criar um
// group a partir de um modelo (ver "Criar grupo" em /admin/autoscaler),
// não a viver junto do modelo -- ver
// services/templatesadmin/internal/store/store.go.
export type ServiceTemplate = {
  name: string;
  image: string;
  cmd: string[];
  env: string[];
  labels: Record<string, string>;
  binds: string[];
  network: string;
  extraHosts: string[];
  // Tamanho aplicado quando quem lança não escolhe nenhum -- vazio cai no
  // default global do launcher.
  defaultInstanceType: string;

  createdAt: string;
  updatedAt: string;
  updatedBy: string;
};

// Catálogo de tamanhos de container, no espírito dos instance types da
// AWS -- ver services/templatesadmin/internal/store/instancetypes.go. O
// launcher traduz vcpu/memoryMb em limites reais de cgroup no momento de
// criar o container.
export type InstanceType = {
  name: string;
  displayName: string;
  vcpu: number;
  memoryMb: number;
  memorySwapMb: number | null;
  pidsLimit: number;
  enabled: boolean;

  createdAt: string;
  updatedAt: string;
  updatedBy: string;
};

async function templatesAdminFetch(path: string, accessToken: string, init?: RequestInit): Promise<Response> {
  return fetch(`${TEMPLATESADMIN_SERVICE_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${accessToken}`, ...init?.headers },
    cache: "no-store",
  });
}

export function listTemplates(accessToken: string) {
  return templatesAdminFetch("/v1/templates", accessToken);
}

export function getTemplate(accessToken: string, name: string) {
  return templatesAdminFetch(`/v1/templates/${encodeURIComponent(name)}`, accessToken);
}

export function upsertTemplate(accessToken: string, name: string, body: Partial<ServiceTemplate>) {
  return templatesAdminFetch(`/v1/templates/${encodeURIComponent(name)}`, accessToken, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}

export function deleteTemplate(accessToken: string, name: string) {
  return templatesAdminFetch(`/v1/templates/${encodeURIComponent(name)}`, accessToken, { method: "DELETE" });
}

export function listBuiltImages(accessToken: string) {
  return templatesAdminFetch("/v1/images", accessToken);
}

export function listInstanceTypes(accessToken: string) {
  return templatesAdminFetch("/v1/instance-types", accessToken);
}

export function upsertInstanceType(accessToken: string, name: string, body: Partial<InstanceType>) {
  return templatesAdminFetch(`/v1/instance-types/${encodeURIComponent(name)}`, accessToken, {
    method: "PUT",
    body: JSON.stringify(body),
  });
}

export function deleteInstanceType(accessToken: string, name: string) {
  return templatesAdminFetch(`/v1/instance-types/${encodeURIComponent(name)}`, accessToken, { method: "DELETE" });
}
