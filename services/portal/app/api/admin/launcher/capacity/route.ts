import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { getCapacity } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await getCapacity(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
