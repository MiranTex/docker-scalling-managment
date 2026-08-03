import { NextResponse } from "next/server";
import { register } from "@/lib/authClient";

export async function POST(req: Request) {
  const { email, password } = await req.json();
  const authRes = await register(email, password);
  const data = await authRes.json().catch(() => ({}));
  return NextResponse.json(data, { status: authRes.status });
}
