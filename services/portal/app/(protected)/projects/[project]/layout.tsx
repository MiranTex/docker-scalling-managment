import { ProjectRoleProvider } from "@/lib/projectRole";

// Carrega uma vez o papel de quem está a ver neste projeto, para todas as
// páginas dele (ambientes, serviços, modelos).
export default async function ProjectLayout({
  params,
  children,
}: {
  params: Promise<{ project: string }>;
  children: React.ReactNode;
}) {
  const { project } = await params;
  return <ProjectRoleProvider project={project}>{children}</ProjectRoleProvider>;
}
