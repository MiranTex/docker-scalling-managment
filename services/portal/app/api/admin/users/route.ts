import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createUser, listUsers } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

// O auth service é quem faz a autorização de verdade (só aceita se a
// claim role do Bearer for "super-admin") -- estes handlers são só um
// proxy fino, igual a app/api/tokens/route.ts.

export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const authRes = await listUsers(accessToken);
  const data = await authRes.json().catch(() => []);
  return NextResponse.json(data, { status: authRes.status });
}

export async function POST(req: Request) {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const { email, password, role } = await req.json();
  const authRes = await createUser(accessToken, email, password, role);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
