import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { createNetwork, listNetworks } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para GET/POST /v1/networks -- lista/cria redes Docker
// definidas pelo utilizador, para escolher na hora de lançar uma
// instância ou criar um grupo (ver services/launcher/internal/dockerclient).
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listNetworks(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function POST(req: NextRequest) {
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await createNetwork(accessToken, body.name);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
