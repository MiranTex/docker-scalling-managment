import { canManageProjects, Forbidden } from "./guard";
import ProjectsClient from "./ProjectsClient";

export default async function ProjectsPage() {
  if (!(await canManageProjects())) return <Forbidden />;
  return <ProjectsClient />;
}
