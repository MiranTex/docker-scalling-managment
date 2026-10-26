import { NextResponse } from "next/server";
import type { TokenPair } from "./authClient";

// Cookies de sessão são todos httpOnly (exceto o e-mail, só para exibição)
// -- os tokens nunca ficam acessíveis a JS no browser, então um XSS na
// página não consegue roubá-los. sameSite=lax é suficiente aqui: não há
// nenhuma ação sensível disparada por um simples GET vindo de outro site.
const COOKIE_OPTS = {
  httpOnly: true,
  sameSite: "lax" as const,
  path: "/",
};

// isHttps diz se o browser chegou por HTTPS -- diretamente ou através do
// Traefik (X-Forwarded-Proto). Só aí os cookies podem ser "Secure": por
// HTTP o browser deita-os fora (exceto em localhost), e o login numa rede
// local sem certificados (portal.<ip>.nip.io) nunca ficava com sessão.
export function isHttps(req: Request): boolean {
  const forwarded = req.headers.get("x-forwarded-proto")?.split(",")[0].trim();
  return forwarded ? forwarded === "https" : new URL(req.url).protocol === "https:";
}

export const ACCESS_COOKIE = "access_token";
export const REFRESH_COOKIE = "refresh_token";
export const EMAIL_COOKIE = "user_email";

export function setSessionCookies(res: NextResponse, pair: TokenPair, secure: boolean, email?: string) {
  const opts = { ...COOKIE_OPTS, secure };
  res.cookies.set(ACCESS_COOKIE, pair.access_token, { ...opts, maxAge: pair.expires_in });
  res.cookies.set(REFRESH_COOKIE, pair.refresh_token, opts);
  if (email) {
    res.cookies.set(EMAIL_COOKIE, email, { ...opts, httpOnly: false });
  }
}

export function clearSessionCookies(res: NextResponse) {
  res.cookies.delete(ACCESS_COOKIE);
  res.cookies.delete(REFRESH_COOKIE);
  res.cookies.delete(EMAIL_COOKIE);
}
