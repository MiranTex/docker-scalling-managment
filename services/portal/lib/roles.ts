// Espelha do lado da UI o bypass universal de super-admin que já existe
// em todo requireRole do lado do servidor (auth, dbadmin, secretsadmin, a
// API admin do autoscaler -- ver services/auth/internal/store/role.go).
// Sem isto, um super-admin passaria nas chamadas de API mas nunca veria
// os links/ecrãs correspondentes no portal, já que a checagem de role
// aqui é só UX -- a autorização de verdade é sempre feita no serviço de
// recursos, não aqui.
export function hasInfraAdminAccess(role?: string): boolean {
  return role === "infra-admin" || role === "super-admin";
}
