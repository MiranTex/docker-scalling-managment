import { canManageProjects, Forbidden } from "../../../guard";
import TemplateClient from "./TemplateClient";

export default async function TemplatePage({ params }: { params: Promise<{ project: string; template: string }> }) {
  if (!(await canManageProjects())) return <Forbidden />;
  const { project, template } = await params;
  return <TemplateClient project={project} template={template} />;
}
