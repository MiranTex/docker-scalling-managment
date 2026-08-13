import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { listContainers } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para listar TODOS os containers do host (não filtrado por
// label) -- alimenta o picker de "ligar este container a esta rede" em
// /admin/networks.
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listContainers(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
