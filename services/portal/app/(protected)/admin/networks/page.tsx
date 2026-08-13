import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";
import NetworksAdminClient from "./NetworksAdminClient";

// A checagem de role aqui é só UX -- a autorização de verdade é feita
// pelo launcher (ver requireRole em services/launcher/internal/httpapi),
// que recusa qualquer chamada cuja claim role não seja "infra-admin" ou
// "super-admin" (bypass universal), independente do que este ecrã
// decidir mostrar.
export default async function AdminNetworksPage() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (!hasInfraAdminAccess(role)) {
    return (
      <div className="card wide">
        <h1>Redes</h1>
        <p className="error">Esta secção é só para infra-admin.</p>
      </div>
    );
  }

  return <NetworksAdminClient />;
}
