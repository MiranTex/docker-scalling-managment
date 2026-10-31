import { isPlatformAdmin, Forbidden } from "../../../projects/guard";
import NodeClient from "./NodeClient";

export default async function NodePage({ params }: { params: Promise<{ node: string }> }) {
  if (!(await isPlatformAdmin())) return <Forbidden />;
  const { node } = await params;
  return <NodeClient node={decodeURIComponent(node)} />;
}
