import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { resetSchemaPassword } from "@/lib/dbAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function POST(_req: NextRequest, { params }: { params: Promise<{ schema: string }> }) {
  const { schema } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await resetSchemaPassword(accessToken, schema);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}