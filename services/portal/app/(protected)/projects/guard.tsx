import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";

// isPlatformAdmin: infra-admin ou super-admin. Só UX -- quem autoriza de
// verdade é o launcher (ver services/launcher/internal/platformapi). As
// páginas de projeto não o usam: qualquer conta vê os projetos de que é
// membro; só as páginas globais (ex: Volumes) são de admins.
export async function isPlatformAdmin(): Promise<boolean> {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;
  return hasInfraAdminAccess(role);
}

export function Forbidden() {
  return (
    <div className="card wide">
      <h1>Sem acesso</h1>
      <p className="error">Esta página é só para administradores da plataforma (infra-admin).</p>
    </div>
  );
}
