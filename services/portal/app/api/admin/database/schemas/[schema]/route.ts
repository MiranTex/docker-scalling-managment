import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { deleteSchema } from "@/lib/dbAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function DELETE(req: NextRequest, { params }: { params: Promise<{ schema: string }> }) {
  const { schema } = await params;
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await deleteSchema(accessToken, schema, body.confirmation ?? "");
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}