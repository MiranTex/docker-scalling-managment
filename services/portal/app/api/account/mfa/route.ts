import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { disableMyMFA, getMyMFA } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

async function accessToken() {
  return (await cookies()).get(ACCESS_COOKIE)!.value;
}

// O estado do 2FA da própria conta.
export async function GET() {
  const authRes = await getMyMFA(await accessToken());
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}

// Desligar o próprio 2FA (com a password; o auth recusa a quem é obrigado).
export async function DELETE(req: Request) {
  const { password } = await req.json();
  const authRes = await disableMyMFA(await accessToken(), password);
  if (authRes.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
