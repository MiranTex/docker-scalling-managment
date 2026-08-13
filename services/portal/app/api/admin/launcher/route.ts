import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { createInstance, listInstances } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// O launcher é quem faz a autorização de verdade (só aceita se a claim
// role do Bearer for "infra-admin") -- este handler é só um proxy fino,
// igual a app/api/admin/templates/route.ts.
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listInstances(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function POST(req: NextRequest) {
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await createInstance(accessToken, body);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
