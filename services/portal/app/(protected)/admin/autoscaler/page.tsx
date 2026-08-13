import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";
import AutoscalerListClient from "./AutoscalerListClient";

// A checagem de role aqui é só UX (evita mostrar o ecrã e depois um erro
// 403 vindo do fetch) -- a autorização de verdade é feita por cada
// instância de autoscaler (ver requireRole em
// services/autoscaler/internal/adminapi), que recusa qualquer chamada
// cuja claim role não seja "infra-admin" ou "super-admin" (bypass
// universal), independente do que este ecrã decidir mostrar.
export default async function AdminAutoscalerPage() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (!hasInfraAdminAccess(role)) {
    return (
      <div className="card wide">
        <h1>Autoscalers</h1>
        <p className="error">Esta secção é só para infra-admin.</p>
      </div>
    );
  }

  return <AutoscalerListClient />;
}
