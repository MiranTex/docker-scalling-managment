import { canManageProjects, Forbidden } from "../../projects/guard";
import VolumesClient from "./VolumesClient";

export default async function VolumesPage() {
  if (!(await canManageProjects())) return <Forbidden />;
  return <VolumesClient />;
}
