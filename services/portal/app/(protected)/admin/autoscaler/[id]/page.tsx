import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";
import AutoscalerDetailClient from "./AutoscalerDetailClient";

// Mesma checagem de role (só UX) de app/(protected)/admin/autoscaler/page.tsx.
export default async function AutoscalerGroupDetailPage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (!hasInfraAdminAccess(role)) {
    return (
      <div className="card wide">
        <h1>Autoscaler-group</h1>
        <p className="error">Esta secção é só para infra-admin.</p>
      </div>
    );
  }

  return <AutoscalerDetailClient id={id} />;
}
