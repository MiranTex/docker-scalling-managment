import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { adminDisableMFA } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Um super-admin desliga o 2FA de uma conta (o auth valida a role e
// termina as sessões dessa conta).
export async function DELETE(_req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const accessToken = (await cookies()).get(ACCESS_COOKIE)!.value;
  const authRes = await adminDisableMFA(accessToken, id);
  if (authRes.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
