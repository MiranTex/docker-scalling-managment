import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { listInstanceTypes } from "@/lib/templatesAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino -- o templatesadmin é quem faz a autorização de verdade (só
// aceita se a claim role do Bearer for "infra-admin").
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listInstanceTypes(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
