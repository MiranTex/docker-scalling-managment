import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { confirmMyMFA } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Confirmar a ativação com o primeiro código; devolve os códigos de
// recuperação.
export async function POST(req: Request) {
  const accessToken = (await cookies()).get(ACCESS_COOKIE)!.value;
  const { code } = await req.json();
  const authRes = await confirmMyMFA(accessToken, code);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
