import { cookies } from "next/headers";
import { NextResponse } from "next/server";
import { listBuiltImages } from "@/lib/templatesAdminClient";
import { ACCESS_COOKIE } from "@/lib/session";

// Proxy fino para a lista de imagens já conhecidas pelo Docker do host
// (ver services/templatesadmin/internal/dockerclient.ListImages) -- é
// daqui que o formulário de modelo popula o <select> de imagem.
export async function GET() {
  const cookieStore = await cookies();
  const accessToken = cookieStore.get(ACCESS_COOKIE)!.value;
  const res = await listBuiltImages(accessToken);
  const data = await res.json().catch(() => ({}));
  return NextResponse.json(data, { status: res.status });
}
