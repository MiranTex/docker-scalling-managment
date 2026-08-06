import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";
import SecretsAdminClient from "./SecretsAdminClient";

// A checagem de role aqui é só UX -- a autorização de verdade é feita
// pelo secretsadmin (ver requireRole em
// services/secretsadmin/internal/httpapi), que recusa qualquer chamada
// cuja claim role não seja "infra-admin" ou "super-admin" (bypass
// universal), independente do que este ecrã decidir mostrar.
export default async function AdminSecretsPage() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (!hasInfraAdminAccess(role)) {
    return (
      <div className="card wide">
        <h1>Segredos</h1>
        <p className="error">Esta secção é só para infra-admin.</p>
      </div>
    );
  }

  return <SecretsAdminClient />;
}
