import { cookies } from "next/headers";
import PageHeader from "@/components/PageHeader";
import SubNav from "@/components/SubNav";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";

const ITEMS = [
  { href: "/admin/database", label: "Visão geral", exact: true },
  { href: "/admin/database/backups", label: "Backups" },
  { href: "/admin/database/schemas", label: "Schemas" },
  { href: "/admin/database/restore", label: "Restore (PITR)" },
];

// A checagem de role aqui é só UX (evita mostrar o ecrã e depois um erro
// 403 vindo do fetch) -- a autorização de verdade é feita pelo dbadmin
// (ver requireRole em services/database/dbadmin/internal/httpapi), que
// recusa qualquer chamada cuja claim role não seja "infra-admin" ou
// "super-admin" (bypass universal), independente do que este ecrã
// decidir mostrar.
export default async function DatabaseLayout({ children }: { children: React.ReactNode }) {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (!hasInfraAdminAccess(role)) {
    return (
      <div className="card wide">
        <h1>Base de dados</h1>
        <p className="error">Esta secção é só para infra-admin.</p>
      </div>
    );
  }

  return (
    <>
      <PageHeader title="Base de dados" description="Estado, backups, schemas e recuperação do PostgreSQL." />
      <SubNav items={ITEMS} />
      {children}
    </>
  );
}
