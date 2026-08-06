import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { getStatus } from "@/lib/dbAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// O dbadmin é quem faz a autorização de verdade (só aceita se a claim
// role do Bearer for "infra-admin") -- este handler é só um proxy fino,
// igual a app/api/admin/users/route.ts.

export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await getStatus(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
