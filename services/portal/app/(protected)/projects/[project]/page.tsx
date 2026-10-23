import ProjectClient from "./ProjectClient";

export default async function ProjectPage({ params }: { params: Promise<{ project: string }> }) {
  const { project } = await params;
  return <ProjectClient project={project} />;
}
