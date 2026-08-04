import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { updateUserRole } from "@/lib/authClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function PATCH(req: Request, { params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const { role } = await req.json();
  const authRes = await updateUserRole(accessToken, id, role);
  if (authRes.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
