// Cliente fino para o secretsadmin (services/secretsadmin, API de
// administração de segredos) -- mesmo padrão de dbAdminClient.ts: só
// chamado do lado do servidor (Route Handlers), SECRETSADMIN_SERVICE_URL
// é interno à rede Docker e nunca deve ser exposto ao browser.
//
// Deliberadamente NÃO existe nenhuma função para ler o VALOR de um
// segredo -- o secretsadmin só devolve valores em claro a contas de
// máquina (role "service", via POST /v1/secrets/resolve), nunca a
// infra-admin/portal. Depois de gravado, um valor só pode ser
// substituído, nunca visto de novo.

export const SECRETSADMIN_SERVICE_URL = process.env.SECRETSADMIN_SERVICE_URL ?? "http://localhost:8092";

export type SecretInfo = {
  name: string;
  created_at: string;
  updated_at: string;
  updated_by: string;
};

async function secretsAdminFetch(path: string, accessToken: string, init?: RequestInit): Promise<Response> {
  return fetch(`${SECRETSADMIN_SERVICE_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${accessToken}`, ...init?.headers },
    cache: "no-store",
  });
}

// Gestão de segredos -- só infra-admin (ver requireRole em
// services/secretsadmin/internal/httpapi).

export function listSecrets(accessToken: string) {
  return secretsAdminFetch("/v1/secrets", accessToken);
}

export function upsertSecret(accessToken: string, name: string, value: string) {
  return secretsAdminFetch(`/v1/secrets/${encodeURIComponent(name)}`, accessToken, {
    method: "PUT",
    body: JSON.stringify({ value }),
  });
}

export function deleteSecret(accessToken: string, name: string) {
  return secretsAdminFetch(`/v1/secrets/${encodeURIComponent(name)}`, accessToken, { method: "DELETE" });
}
