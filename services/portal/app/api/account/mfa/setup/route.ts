import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { startMyMFA } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Começar a ativar o 2FA: o segredo e o link para o QR.
export async function POST() {
  const accessToken = (await cookies()).get(ACCESS_COOKIE)!.value;
  const authRes = await startMyMFA(accessToken);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
