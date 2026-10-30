import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { setUserPassword } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Um super-admin define a password de uma conta (o auth valida a role e
// revoga as sessões dessa conta).
export async function PUT(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const { password } = await req.json();
  const authRes = await setUserPassword(accessToken, id, password);
  if (authRes.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
