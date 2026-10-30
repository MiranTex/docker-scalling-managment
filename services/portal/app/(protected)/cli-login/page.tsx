import CLILoginClient from "./CLILoginClient";

export default async function CLILoginPage({ searchParams }: { searchParams: Promise<{ code?: string }> }) {
  const { code } = await searchParams;
  return <CLILoginClient code={(code ?? "").toUpperCase()} />;
}
