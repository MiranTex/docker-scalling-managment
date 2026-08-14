// Cliente fino para a API admin de instâncias do autoscaler group (ver
// services/autoscaler/internal/adminapi) -- mesmo padrão de
// dbAdminClient.ts: só chamado do lado do servidor (Route Handlers), os
// URLs das instâncias são internos à rede Docker e nunca devem ser
// expostos ao browser.
//
// Ao contrário do dbadmin (uma BD partilhada só, um URL fixo), pode haver
// VÁRIAS instâncias de autoscaler no sistema -- uma por serviço gerido.
// Descobertas dinamicamente contra o launcher (ver
// lib/launcherClient.listGroups, GET /v1/groups) -- não há mais lista
// estática (AUTOSCALER_ADMIN_INSTANCES foi retirado).

import { listGroups, type DiscoveredGroup } from "./launcherClient";

export type AutoscalerApiError = { error: string; message: string };

export type Replica = {
  container_id: string;
  name: string;
  state: string;
  status: string;
  cpu_percent: number;
};

export type LaunchTemplateInfo = {
  path: string;
  sha256?: string;
  modified_at?: string;
};

export type ScaleAction = {
  action: string;
  at: string;
  reason: string;
};

export type PolicyDTO = {
  min_replicas?: number;
  max_replicas?: number;
  cpu_scale_up_percent?: number;
  cpu_scale_down_percent?: number;
  sustained_ticks?: number;
  cooldown_seconds?: number;
};

export type AutoscalerStatus = {
  target_service: string;
  replica_count: number;
  replicas: Replica[];
  policy: PolicyDTO;
  launch_template: LaunchTemplateInfo;
  last_scale_action?: ScaleAction;
  uptime_seconds: number;
};

// InstanceSummary é uma linha de GET /api/admin/autoscaler (todos os
// groups) e o corpo de GET /api/admin/autoscaler/{instanceId} (um só) --
// mesma forma nos dois, para a página de detalhe conseguir reaproveitar o
// tipo da listagem. id é o containerId (ver findGroup), nunca um id
// gerado por este portal.
export type InstanceSummary = {
  id: string;
  label: string;
  url: string;
  network: string;
  status?: AutoscalerStatus;
  error?: string;
  // exposedHost -- ver lib/launcherClient.DiscoveredGroup.exposedHost.
  exposedHost?: string;
};

// findGroup pergunta ao launcher quais groups estão vivos agora (GET
// /v1/groups) e devolve o que tem este containerId -- é assim que as
// rotas de UMA instância (policy/restart/réplicas) resolvem o
// "instanceId" da URL para o adminUrl real a chamar, sem depender de
// nenhuma lista estática. undefined se não existir/launcher inalcançável
// -- quem chama decide responder 404.
export async function findGroup(accessToken: string, containerId: string): Promise<DiscoveredGroup | undefined> {
  try {
    const res = await listGroups(accessToken);
    if (!res.ok) return undefined;
    const groups: DiscoveredGroup[] = await res.json();
    return groups.find((g) => g.containerId === containerId);
  } catch {
    return undefined;
  }
}

async function autoscalerFetch(instanceUrl: string, path: string, accessToken: string, init?: RequestInit): Promise<Response> {
  return fetch(`${instanceUrl}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${accessToken}`, ...init?.headers },
    cache: "no-store",
  });
}

// Gestão do autoscaler -- só infra-admin (ver requireRole em
// services/autoscaler/internal/adminapi). accessToken precisa ter a claim
// role == "infra-admin", senão a API responde 403.

export function getStatus(instanceUrl: string, accessToken: string) {
  return autoscalerFetch(instanceUrl, "/v1/status", accessToken);
}

export function getPolicy(instanceUrl: string, accessToken: string) {
  return autoscalerFetch(instanceUrl, "/v1/policy", accessToken);
}

export function updatePolicy(instanceUrl: string, accessToken: string, patch: PolicyDTO) {
  return autoscalerFetch(instanceUrl, "/v1/policy", accessToken, { method: "PUT", body: JSON.stringify(patch) });
}

// restart é lógico e interno ao processo do group (termina e reconstrói as
// réplicas geridas a partir do launch template como ele estiver em disco
// agora) -- não depende de o Docker reiniciar o container, funciona da
// mesma forma em qualquer ambiente. Ver services/autoscaler/cmd/group/restart.go.
export function restart(instanceUrl: string, accessToken: string) {
  return autoscalerFetch(instanceUrl, "/v1/restart", accessToken, { method: "POST" });
}

// addReplica/removeReplica são ações manuais sobre UMA réplica específica
// -- ao contrário do scaler automático, aqui é quem chama que escolhe
// (ver services/autoscaler/internal/adminapi, POST/DELETE /v1/replicas).
export function addReplica(instanceUrl: string, accessToken: string) {
  return autoscalerFetch(instanceUrl, "/v1/replicas", accessToken, { method: "POST" });
}

export function removeReplica(instanceUrl: string, accessToken: string, replicaId: string) {
  return autoscalerFetch(instanceUrl, `/v1/replicas/${encodeURIComponent(replicaId)}`, accessToken, { method: "DELETE" });
}
