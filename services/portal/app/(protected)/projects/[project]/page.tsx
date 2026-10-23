import { canManageProjects, Forbidden } from "../guard";
import ProjectClient from "./ProjectClient";

export default async function ProjectPage({ params }: { params: Promise<{ project: string }> }) {
  if (!(await canManageProjects())) return <Forbidden />;
  const { project } = await params;
  return <ProjectClient project={project} />;
}
