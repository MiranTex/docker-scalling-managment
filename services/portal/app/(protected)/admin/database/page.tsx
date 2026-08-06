import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { ACCESS_COOKIE } from "@/lib/session";
import DatabaseAdminClient from "./DatabaseAdminClient";

// A checagem de role aqui é só UX (evita mostrar o ecrã e depois um erro
// 403 vindo do fetch) -- a autorização de verdade é feita pelo dbadmin
// (ver requireRole em services/database/dbadmin/internal/httpapi), que
// recusa qualquer chamada cuja claim role não seja "infra-admin",
// independente do que este ecrã decidir mostrar.
export default async function AdminDatabasePage() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (role !== "infra-admin") {
    return (
      <div className="card wide">
        <h1>Base de dados</h1>
        <p className="error">Esta secção é só para infra-admin.</p>
      </div>
    );
  }

  return <DatabaseAdminClient />;
}
