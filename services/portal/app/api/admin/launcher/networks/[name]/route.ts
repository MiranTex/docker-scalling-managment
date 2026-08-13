import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { deleteNetwork, getNetwork } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para GET/DELETE de UMA rede -- GET devolve os containers
// ligados a ela agora (ver services/launcher/internal/dockerclient.NetworkDetail);
// DELETE só funciona se o Docker não recusar por ainda haver algum
// container ligado.
export async function GET(_req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await getNetwork(accessToken, name);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}

export async function DELETE(_req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await deleteNetwork(accessToken, name);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
