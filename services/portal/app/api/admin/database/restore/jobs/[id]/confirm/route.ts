import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { confirmRestore } from "@/lib/dbAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function POST(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await confirmRestore(accessToken, id);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
