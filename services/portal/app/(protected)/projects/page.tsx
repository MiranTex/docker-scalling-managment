import { isPlatformAdmin } from "./guard";
import ProjectsClient from "./ProjectsClient";

// Qualquer conta entra: o launcher só devolve os projetos de que ela é
// membro (todos, para admins da plataforma).
export default async function ProjectsPage() {
  return <ProjectsClient canCreate={await isPlatformAdmin()} />;
}
