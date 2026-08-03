import { NextResponse } from "next/server";
import type { TokenPair } from "./authClient";

// Cookies de sessão são todos httpOnly (exceto o e-mail, só para exibição)
// -- os tokens nunca ficam acessíveis a JS no browser, então um XSS na
// página não consegue roubá-los. sameSite=lax é suficiente aqui: não há
// nenhuma ação sensível disparada por um simples GET vindo de outro site.
const COOKIE_OPTS = {
  httpOnly: true,
  sameSite: "lax" as const,
  secure: process.env.NODE_ENV === "production",
  path: "/",
};

export const ACCESS_COOKIE = "access_token";
export const REFRESH_COOKIE = "refresh_token";
export const EMAIL_COOKIE = "user_email";

export function setSessionCookies(res: NextResponse, pair: TokenPair, email?: string) {
  res.cookies.set(ACCESS_COOKIE, pair.access_token, { ...COOKIE_OPTS, maxAge: pair.expires_in });
  res.cookies.set(REFRESH_COOKIE, pair.refresh_token, COOKIE_OPTS);
  if (email) {
    res.cookies.set(EMAIL_COOKIE, email, { ...COOKIE_OPTS, httpOnly: false });
  }
}

export function clearSessionCookies(res: NextResponse) {
  res.cookies.delete(ACCESS_COOKIE);
  res.cookies.delete(REFRESH_COOKIE);
  res.cookies.delete(EMAIL_COOKIE);
}
