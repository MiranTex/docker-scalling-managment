import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { listBackups, triggerBackup } from "@/lib/dbAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listBackups(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function POST(req: Request) {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const { type } = await req.json();
  const res = await triggerBackup(accessToken, type);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
