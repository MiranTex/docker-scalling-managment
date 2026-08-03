import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "base-stack portal",
  description: "Login, sessão e API tokens pessoais",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="pt">
      <body>{children}</body>
    </html>
  );
}
