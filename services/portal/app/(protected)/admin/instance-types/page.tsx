import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";
import InstanceTypesClient from "./InstanceTypesClient";

// A checagem de role aqui é só UX -- a autorização de verdade é feita
// pelo templatesadmin (ver requireRole em
// services/templatesadmin/internal/httpapi).
export default async function AdminInstanceTypesPage() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (!hasInfraAdminAccess(role)) {
    return (
      <div className="card wide">
        <h1>Tipos de instância</h1>
        <p className="error">Esta secção é só para infra-admin.</p>
      </div>
    );
  }

  return <InstanceTypesClient />;
}
