import { NextResponse, type NextRequest } from "next/server";
import { refreshTokens, type TokenPair } from "./lib/authClient";
import { ACCESS_COOKIE, REFRESH_COOKIE, setSessionCookies, clearSessionCookies } from "./lib/session";

// Guarda as rotas protegidas: se o access token (curto, 15 min por
// default) já expirou mas o refresh token ainda vale, renova a sessão
// aqui -- um único lugar, em vez de replicar essa lógica em cada Route
// Handler que fala com o auth service.
export async function middleware(req: NextRequest) {
  const accessToken = req.cookies.get(ACCESS_COOKIE)?.value;
  if (accessToken) {
    return NextResponse.next();
  }

  const refreshToken = req.cookies.get(REFRESH_COOKIE)?.value;
  if (refreshToken) {
    try {
      const res = await refreshTokens(refreshToken);
      if (res.ok) {
        const pair: TokenPair = await res.json();
        // Não basta pôr o cookie novo na resposta (isso só chega ao
        // browser no PRÓXIMO pedido) -- sem também mutar req.cookies e
        // repassar req.headers, o Route Handler/Server Component que vai
        // tratar este MESMO pedido ainda veria access_token ausente (e
        // `cookies().get(ACCESS_COOKIE)!.value` rebentaria em runtime nos
        // handlers que assumem que o middleware já garantiu o cookie).
        req.cookies.set(ACCESS_COOKIE, pair.access_token);
        req.cookies.set(REFRESH_COOKIE, pair.refresh_token);
        const next = NextResponse.next({ request: { headers: req.headers } });
        setSessionCookies(next, pair);
        return next;
      }
    } catch {
      // auth service inalcançável -- cai para o redirect de login abaixo.
    }
  }

  if (req.nextUrl.pathname.startsWith("/api/")) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const loginUrl = new URL("/login", req.url);
  loginUrl.searchParams.set("from", req.nextUrl.pathname);
  const redirect = NextResponse.redirect(loginUrl);
  clearSessionCookies(redirect);
  return redirect;
}

export const config = {
  matcher: ["/dashboard/:path*", "/tokens/:path*", "/admin/:path*", "/api/tokens/:path*", "/api/admin/:path*"],
};
