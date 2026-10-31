import AddonClient from "./AddonClient";

export default async function AddonPage({ params }: { params: Promise<{ project: string; env: string; addon: string }> }) {
  const { project, env, addon } = await params;
  return <AddonClient project={project} env={env} addon={addon} />;
}
