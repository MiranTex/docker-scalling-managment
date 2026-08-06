// Cliente fino para a API admin de instâncias do autoscaler group (ver
// services/autoscaler/internal/adminapi) -- mesmo padrão de
// dbAdminClient.ts: só chamado do lado do servidor (Route Handlers), os
// URLs das instâncias são internos à rede Docker e nunca devem ser
// expostos ao browser.
//
// Ao contrário do dbadmin (uma BD partilhada só, um URL fixo), pode haver
// VÁRIAS instâncias de autoscaler no sistema -- uma por serviço gerido,
// possivelmente em ambientes/hosts diferentes. Sem registo central (ver
// plano): cada ambiente do portal só conhece as instâncias que lhe
// pertencem, configuradas como uma lista estática em
// AUTOSCALER_ADMIN_INSTANCES (JSON).

export type AutoscalerInstance = { id: string; label: string; url: string };

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

// getInstances lê a lista estática de instâncias configuradas para este
// ambiente do portal. JSON inválido/ausente resulta em lista vazia (a UI
// mostra "nenhum autoscaler configurado"), não num erro 500 -- um erro de
// configuração aqui não deveria derrubar o resto do painel admin.
export function getInstances(): AutoscalerInstance[] {
  const raw = process.env.AUTOSCALER_ADMIN_INSTANCES;
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (v): v is AutoscalerInstance =>
        typeof v === "object" && v !== null && typeof v.id === "string" && typeof v.label === "string" && typeof v.url === "string"
    );
  } catch {
    return [];
  }
}

export function getInstance(instanceId: string): AutoscalerInstance | undefined {
  return getInstances().find((i) => i.id === instanceId);
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
