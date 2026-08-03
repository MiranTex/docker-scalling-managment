// Decodifica só o payload de um JWT, sem validar assinatura -- o portal
// nunca precisa validar o token (isso é trabalho do auth service e de
// quem consome a API), só ler claims para exibir na UI (sub, exp).
export function decodeJwtPayload(token: string): Record<string, unknown> | null {
  const parts = token.split(".");
  if (parts.length !== 3) return null;
  try {
    const base64 = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    const json = Buffer.from(base64, "base64").toString("utf-8");
    return JSON.parse(json);
  } catch {
    return null;
  }
}
