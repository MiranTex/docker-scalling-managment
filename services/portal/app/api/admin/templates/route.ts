import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { listTemplates } from "@/lib/templatesAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// O templatesadmin é quem faz a autorização de verdade (só aceita se a
// claim role do Bearer for "infra-admin") -- este handler é só um proxy
// fino, igual a app/api/admin/secrets/route.ts.
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listTemplates(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
