import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { ACCESS_COOKIE } from "@/lib/session";
import AdminUsersClient from "./AdminUsersClient";

// A checagem de role aqui é só UX (evita mostrar a tabela e depois um
// erro 403 vindo do fetch) -- a autorização de verdade é feita pelo auth
// service (ver requireRole em services/auth/internal/httpapi), que
// recusa qualquer chamada a /v1/admin/* cuja claim role não seja
// "super-admin", independente do que este ecrã decidir mostrar.
export default async function AdminUsersPage() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (role !== "super-admin") {
    return (
      <div className="card wide">
        <h1>Gestão de utilizadores</h1>
        <p className="error">Esta secção é só para super-admin.</p>
      </div>
    );
  }

  return <AdminUsersClient />;
}
