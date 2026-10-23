import { canManageProjects, Forbidden } from "../../guard";
import EnvironmentClient from "./EnvironmentClient";

export default async function EnvironmentPage({ params }: { params: Promise<{ project: string; env: string }> }) {
  if (!(await canManageProjects())) return <Forbidden />;
  const { project, env } = await params;
  return <EnvironmentClient project={project} env={env} />;
}
