import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { disconnectNetwork } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para desligar um container de uma rede -- não o pára nem o
// remove, ver services/launcher/internal/httpapi.handleDisconnectNetwork.
export async function POST(req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await disconnectNetwork(accessToken, name, body.containerId);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
