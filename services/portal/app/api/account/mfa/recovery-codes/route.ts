import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { newRecoveryCodes } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Códigos de recuperação novos (os antigos deixam de servir), com um
// código da app.
export async function POST(req: Request) {
  const accessToken = (await cookies()).get(ACCESS_COOKIE)!.value;
  const { code } = await req.json();
  const authRes = await newRecoveryCodes(accessToken, code);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
