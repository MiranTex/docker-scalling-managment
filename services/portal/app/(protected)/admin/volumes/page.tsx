import { isPlatformAdmin, Forbidden } from "../../projects/guard";
import VolumesClient from "./VolumesClient";

export default async function VolumesPage() {
  if (!(await isPlatformAdmin())) return <Forbidden />;
  return <VolumesClient />;
}
