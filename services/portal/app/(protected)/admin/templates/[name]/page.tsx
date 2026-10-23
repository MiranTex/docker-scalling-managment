import { cookies } from "next/headers";
import { decodeJwtPayload } from "@/lib/jwt";
import { hasInfraAdminAccess } from "@/lib/roles";
import { ACCESS_COOKIE } from "@/lib/session";
import TemplateFormClient from "../TemplateFormClient";

// Role check só de UX; a autorização real é feita pelo templatesadmin.
export default async function EditTemplatePage({ params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)?.value;
  const role = accessToken ? (decodeJwtPayload(accessToken)?.role as string | undefined) : undefined;

  if (!hasInfraAdminAccess(role)) {
    return <p className="error">Esta secção é só para infra-admin.</p>;
  }

  return <TemplateFormClient name={decodeURIComponent(name)} />;
}
