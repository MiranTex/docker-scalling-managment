import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";
import { connectNetwork } from "@/lib/launcherClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para ligar um container já em execução a uma rede, sem o
// recriar -- ver services/launcher/internal/httpapi.handleConnectNetwork.
export async function POST(req: NextRequest, { params }: { params: Promise<{ name: string }> }) {
  const { name } = await params;
  const body = await req.json().catch(() => ({}));
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await connectNetwork(accessToken, name, body.containerId);
  if (res.status === 204) {
    return new NextResponse(null, { status: 204 });
  }
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
