import { isPlatformAdmin, Forbidden } from "../../projects/guard";
import NodesClient from "./NodesClient";

export default async function NodesPage() {
  if (!(await isPlatformAdmin())) return <Forbidden />;
  return <NodesClient />;
}
