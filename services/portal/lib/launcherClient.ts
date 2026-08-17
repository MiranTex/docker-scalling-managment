// Cliente fino para o launcher (services/launcher, o serviço que
// efetivamente cria containers a partir de um modelo) -- mesmo padrão de
// templatesAdminClient.ts: só chamado do lado do servidor (Route
// Handlers), LAUNCHER_SERVICE_URL é interno à rede Docker e nunca deve
// ser exposto ao browser.

export const LAUNCHER_SERVICE_URL = process.env.LAUNCHER_SERVICE_URL ?? "http://localhost:8094";

export type LauncherInstance = {
  id: string;
  kind: "solo" | "group";
  templateName: string;
  containerId: string;
  status: "running" | "stopped" | "failed";
  error?: string;
  createdAt: string;
  updatedAt: string;
  updatedBy: string;
  stoppedAt?: string;
  // exposedHost é o hostname público desta instância (ver
  // services/launcher/internal/httpapi.labelExposeHost), lido ao vivo do
  // container -- ausente/vazio se nunca foi exposta.
  exposedHost?: string;
  // exposedScheme -- "https" ou "http", conforme o launcher tinha
  // PUBLIC_TLS_CERT_RESOLVER configurado no momento em que esta instância
  // foi exposta (ver httpapi.labelExposeScheme). Ausente se nunca foi
  // exposta; tratar como "http" nesse caso.
  exposedScheme?: string;
};

export type CreateInstanceRequest = {
  templateName: string;
  kind: "solo" | "group";
  // Se preenchido, substitui a rede Docker do modelo só para esta
  // instância -- sem isto, um modelo sem rede definida cria o container
  // na rede "bridge" do Docker, isolada do launcher/portal/database, e a
  // API admin da instância fica inatingível. Ver lib/launcherClient.listNetworks
  // para escolher/criar uma em vez de digitar à mão.
  network?: string;
  // extraNetworks liga a instância a redes ADICIONAIS, além de "network"
  // -- ex: a rede da própria aplicação E "observability-net" (ver
  // services/monitoring) ao mesmo tempo, sem precisar voltar a
  // /admin/networks depois. Mesma validação de sempre no launcher (só
  // redes "base-stack.managed").
  extraNetworks?: string[];
  // Campos abaixo só são lidos pelo launcher quando kind="group" -- ver
  // services/launcher/internal/httpapi.createInstanceRequest. Um modelo
  // (ServiceTemplate) já não tem estes campos; é aqui, ao criar o group,
  // que se decide como ele escala.
  targetService?: string;
  backendPort?: number;
  minReplicas?: number;
  maxReplicas?: number;
  cpuScaleUpPercent?: number;
  cpuScaleDownPercent?: number;
  replicaAuthToken?: string;
  // exposeAs expõe esta instância publicamente via Traefik, em
  // "<exposeAs>.<PUBLIC_BASE_DOMAIN>" -- nunca publica porta no host, só
  // escreve labels que o Traefik já observa (ver demo/docker-compose.yml).
  // exposePort é obrigatório junto com exposeAs quando kind="solo" (a
  // imagem é arbitrária, sem convenção de porta); ignorado para "group",
  // que expõe sempre o seu próprio proxy interno.
  exposeAs?: string;
  exposePort?: number;
};

async function launcherFetch(path: string, accessToken: string, init?: RequestInit): Promise<Response> {
  return fetch(`${LAUNCHER_SERVICE_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${accessToken}`, ...init?.headers },
    cache: "no-store",
  });
}

export function listInstances(accessToken: string) {
  return launcherFetch("/v1/instances", accessToken);
}

export function createInstance(accessToken: string, body: CreateInstanceRequest) {
  return launcherFetch("/v1/instances", accessToken, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

// ReplicaSummary é uma linha de GET /v1/replicas -- uma réplica de
// aplicação viva, gerida por algum autoscaler-group, descoberta pelo
// launcher contra o Docker (pela mesma label que cada cmd/group injeta no
// seu launch template), nunca a partir da store dele -- ver
// services/launcher/internal/httpapi.handleListReplicas. É o que faz uma
// réplica lançada pelo autoscaler aparecer em /admin/launcher, além do seu
// próprio group em /admin/autoscaler.
export type ReplicaSummary = {
  containerId: string;
  name: string;
  targetService: string;
  image: string;
  state: string;
  network: string;
  // exposedHost/exposedScheme -- ver LauncherInstance.
  exposedHost?: string;
  exposedScheme?: string;
};

export function listReplicas(accessToken: string) {
  return launcherFetch("/v1/replicas", accessToken);
}

// stopContainer para (sem remover) um container desta plataforma pelo seu
// containerId -- funciona para uma instância "solo" ou uma réplica (nunca
// para um group inteiro, ver deleteGroup). O container continua a existir,
// só parado.
export function stopContainer(accessToken: string, containerId: string) {
  return launcherFetch(`/v1/containers/${encodeURIComponent(containerId)}/stop`, accessToken, { method: "POST" });
}

// removeContainer para e remove um container desta plataforma pelo seu
// containerId -- mesmo alcance de stopContainer, mas elimina o container.
export function removeContainer(accessToken: string, containerId: string) {
  return launcherFetch(`/v1/containers/${encodeURIComponent(containerId)}`, accessToken, { method: "DELETE" });
}

// DiscoveredGroup é uma linha de GET /v1/groups -- um container de
// autoscaler-group vivo, descoberto pelo launcher contra o Docker (por
// label, ver services/launcher/internal/httpapi.labelGroupRole), não a
// partir da store dele. Inclui tanto groups lançados por este launcher
// como estáticos num docker-compose.yml (ex: group-authd), desde que
// tenham a mesma label.
export type DiscoveredGroup = {
  containerId: string;
  name: string;
  targetService: string;
  image: string;
  state: string;
  network: string;
  // adminUrl é o endereço da API admin desse group (ver
  // services/autoscaler/internal/adminapi) -- é para aí, não para o
  // launcher, que o portal manda pedidos de policy/restart/réplicas.
  adminUrl: string;
  // exposedHost/exposedScheme -- ver LauncherInstance.
  exposedHost?: string;
  exposedScheme?: string;
};

export function listGroups(accessToken: string) {
  return launcherFetch("/v1/groups", accessToken);
}

// deleteGroup para e remove o container de UM autoscaler-group,
// identificado pelo containerId (não pelo id de instância do launcher --
// nem todo group foi lançado por ele, ex: group-authd). É esta função,
// não deleteInstance, que o ecrã /admin/autoscaler usa para "Parar
// grupo" -- deliberadamente não exposta em /admin/launcher (ver
// LauncherAdminClient.tsx).
export function deleteGroup(accessToken: string, containerId: string) {
  return launcherFetch(`/v1/groups/${encodeURIComponent(containerId)}`, accessToken, { method: "DELETE" });
}

// DockerNetwork é uma linha de GET /v1/networks -- uma rede Docker
// definida pelo utilizador (nunca as três automáticas do Docker, ver
// services/launcher/internal/dockerclient.ListNetworks). É o que
// alimenta o <select> de rede na hora de lançar uma instância ou criar
// um grupo, em vez de um campo de texto livre.
export type DockerNetwork = {
  Id: string;
  Name: string;
  Driver: string;
  Scope: string;
};

export function listNetworks(accessToken: string) {
  return launcherFetch("/v1/networks", accessToken);
}

// createNetwork cria uma rede Docker nova, sem subnet nem opções de
// driver -- o Docker escolhe tudo isso sozinho.
export function createNetwork(accessToken: string, name: string) {
  return launcherFetch("/v1/networks", accessToken, { method: "POST", body: JSON.stringify({ name }) });
}

// NetworkContainer é um dos containers ligados a uma rede, dentro de
// NetworkDetail -- só o suficiente para mostrar/desligar.
export type NetworkContainer = { containerId: string; name: string };

// NetworkDetail é uma rede com os containers atualmente ligados a ela --
// "editar" uma rede, no sentido em que o Docker permite (nome/subnet/
// driver são fixos desde a criação): ligar/desligar containers.
export type NetworkDetail = DockerNetwork & { containers: NetworkContainer[] };

export function getNetwork(accessToken: string, name: string) {
  return launcherFetch(`/v1/networks/${encodeURIComponent(name)}`, accessToken);
}

// deleteNetwork apaga uma rede -- o Docker recusa se ainda houver algum
// container ligado a ela (erro repassado tal como veio).
export function deleteNetwork(accessToken: string, name: string) {
  return launcherFetch(`/v1/networks/${encodeURIComponent(name)}`, accessToken, { method: "DELETE" });
}

export function connectNetwork(accessToken: string, name: string, containerId: string) {
  return launcherFetch(`/v1/networks/${encodeURIComponent(name)}/connect`, accessToken, {
    method: "POST",
    body: JSON.stringify({ containerId }),
  });
}

export function disconnectNetwork(accessToken: string, name: string, containerId: string) {
  return launcherFetch(`/v1/networks/${encodeURIComponent(name)}/disconnect`, accessToken, {
    method: "POST",
    body: JSON.stringify({ containerId }),
  });
}

// ContainerSummary é uma linha de GET /v1/containers -- TODO container do
// host (não filtrado por label, ao contrário de listGroups) -- é o que
// alimenta o picker de "ligar este container a esta rede".
export type ContainerSummary = {
  containerId: string;
  name: string;
  image: string;
  state: string;
  networks: string[];
};

export function listContainers(accessToken: string) {
  return launcherFetch("/v1/containers", accessToken);
}
