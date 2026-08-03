// Cliente fino para o auth service (services/auth) — usado só do lado do
// servidor (Route Handlers, middleware). Nunca é importado por um Client
// Component: AUTH_SERVICE_URL é interno à rede Docker e não deve ser
// exposto ao browser.

export const AUTH_SERVICE_URL = process.env.AUTH_SERVICE_URL ?? "http://localhost:8081";

export type TokenPair = {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_in: number;
};

export type ApiKeyListItem = {
  id: string;
  scopes: string[] | null;
  created_at: string;
  expires_at?: string;
  revoked_at?: string;
  status: "active" | "revoked" | "expired";
};

export type ApiKeyCreated = {
  id: string;
  key: string;
  scopes: string[] | null;
  expires_at?: string;
};

export type AuthApiError = { error: string; message: string };

async function authFetch(path: string, init?: RequestInit): Promise<Response> {
  return fetch(`${AUTH_SERVICE_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init?.headers },
    cache: "no-store",
  });
}

export function login(email: string, password: string) {
  return authFetch("/v1/login", { method: "POST", body: JSON.stringify({ email, password }) });
}

export function register(email: string, password: string) {
  return authFetch("/v1/register", { method: "POST", body: JSON.stringify({ email, password }) });
}

export function refreshTokens(refreshToken: string) {
  return authFetch("/v1/token/refresh", {
    method: "POST",
    body: JSON.stringify({ refresh_token: refreshToken }),
  });
}

export function logout(refreshToken: string) {
  return authFetch("/v1/logout", { method: "POST", body: JSON.stringify({ refresh_token: refreshToken }) });
}

export function listApiKeys(accessToken: string) {
  return authFetch("/v1/api-keys", { headers: { Authorization: `Bearer ${accessToken}` } });
}

export function createApiKey(accessToken: string, scopes: string[], ttlSeconds?: number) {
  return authFetch("/v1/api-keys", {
    method: "POST",
    headers: { Authorization: `Bearer ${accessToken}` },
    body: JSON.stringify({ scopes, ttl_seconds: ttlSeconds }),
  });
}

export function revokeApiKey(accessToken: string, id: string) {
  return authFetch(`/v1/api-keys/${id}`, {
    method: "DELETE",
    headers: { Authorization: `Bearer ${accessToken}` },
  });
}
