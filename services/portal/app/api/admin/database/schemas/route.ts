import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { createSchema, listSchemas } from "@/lib/dbAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listSchemas(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function POST(req: Request) {
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await createSchema(accessToken, body.schema_name ?? "", body.role_name ?? "");
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}