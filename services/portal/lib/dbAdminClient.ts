// Cliente fino para o dbadmin (services/database, API de administração
// da BD) -- mesmo padrão de authClient.ts: só chamado do lado do
// servidor (Route Handlers), DBADMIN_SERVICE_URL é interno à rede
// Docker e nunca deve ser exposto ao browser.

export const DBADMIN_SERVICE_URL = process.env.DBADMIN_SERVICE_URL ?? "http://localhost:8091";

export type DbAdminApiError = { error: string; message: string };

export type DbStatus = {
  postgres: {
    version: string;
    database_name: string;
    database_size_bytes: number;
    connection_count: number;
    start_time: string;
  };
  last_backup_at: string | null;
  backup_info_error?: string;
};

export type Backup = {
  label: string;
  type: "full" | "diff" | "incr";
  timestamp: { start: number; stop: number };
  info: { size: number };
  error: boolean;
};

export type StanzaInfo = {
  name: string;
  status: { message: string };
  backup: Backup[];
};

export type BackupJob = {
  id: string;
  type: string;
  status: "running" | "succeeded" | "failed";
  started_at: string;
  ended_at?: string;
  error?: string;
  started_by: string;
};

export type RestoreJob = {
  id: string;
  type: string;
  target: string;
  status: "running" | "awaiting_confirmation" | "succeeded" | "failed";
  started_at: string;
  ended_at?: string;
  error?: string;
  started_by: string;
  confirmed_by?: string;
};

async function dbAdminFetch(path: string, accessToken: string, init?: RequestInit): Promise<Response> {
  return fetch(`${DBADMIN_SERVICE_URL}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", Authorization: `Bearer ${accessToken}`, ...init?.headers },
    cache: "no-store",
  });
}

// Gestão da base de dados -- só infra-admin (ver requireRole em
// services/database/dbadmin/internal/httpapi). accessToken precisa ter a
// claim role == "infra-admin", senão o dbadmin responde 403.

export function getStatus(accessToken: string) {
  return dbAdminFetch("/v1/status", accessToken);
}

export function listBackups(accessToken: string) {
  return dbAdminFetch("/v1/backups", accessToken);
}

export function triggerBackup(accessToken: string, type: "full" | "diff" | "incr") {
  return dbAdminFetch("/v1/backups", accessToken, { method: "POST", body: JSON.stringify({ type }) });
}

export function getBackupJob(accessToken: string, id: string) {
  return dbAdminFetch(`/v1/backups/jobs/${id}`, accessToken);
}

// PITR -- irreversível a partir do momento em que é disparado (ver
// services/database/README.md). A confirmação "escreve o nome da BD"
// acontece na UI (DatabaseAdminClient), antes deste pedido sequer sair
// do browser -- isto é só o transporte.

export function triggerRestore(accessToken: string, type: string, target: string) {
  return dbAdminFetch("/v1/restore", accessToken, { method: "POST", body: JSON.stringify({ type, target }) });
}

export function getRestoreJob(accessToken: string, id: string) {
  return dbAdminFetch(`/v1/restore/jobs/${id}`, accessToken);
}

export function confirmRestore(accessToken: string, id: string) {
  return dbAdminFetch(`/v1/restore/jobs/${id}/confirm`, accessToken, { method: "POST" });
}
