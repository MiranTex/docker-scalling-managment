import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { triggerRestore } from "@/lib/dbAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function POST(req: Request) {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const { type, target } = await req.json();
  const res = await triggerRestore(accessToken, type, target);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
