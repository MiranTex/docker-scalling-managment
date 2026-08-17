import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { mintServiceTokens } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function POST(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const authRes = await mintServiceTokens(accessToken, id);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}