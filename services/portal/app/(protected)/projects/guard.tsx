import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";

// Só UX -- quem autoriza de verdade é o launcher (requireAdmin em
// services/launcher/internal/platformapi).
export async function canManageProjects(): Promise<boolean> {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;
  return hasInfraAdminAccess(role);
}

export function Forbidden() {
  return (
    <div className="card wide">
      <h1>Projetos</h1>
      <p className="error">Esta secção é só para infra-admin.</p>
    </div>
  );
}
