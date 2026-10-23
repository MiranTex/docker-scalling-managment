import { canManageProjects, Forbidden } from "../../../../guard";
import ServiceClient from "./ServiceClient";

export default async function ServicePage({
  params,
}: {
  params: Promise<{ project: string; env: string; svc: string }>;
}) {
  if (!(await canManageProjects())) return <Forbidden />;
  const { project, env, svc } = await params;
  return <ServiceClient project={project} env={env} svc={svc} />;
}
