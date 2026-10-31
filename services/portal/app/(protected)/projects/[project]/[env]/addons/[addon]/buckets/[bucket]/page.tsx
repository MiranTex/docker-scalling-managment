import BucketClient from "./BucketClient";

export default async function BucketPage({
  params,
}: {
  params: Promise<{ project: string; env: string; addon: string; bucket: string }>;
}) {
  const { project, env, addon, bucket } = await params;
  return <BucketClient project={project} env={env} addon={addon} bucket={bucket} />;
}
