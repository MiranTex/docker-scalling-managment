import { isPlatformAdmin } from "../projects/guard";
import MetricsClient from "./MetricsClient";

// Ponto de entrada de "Métricas e logs": no Grafana cada projeto é uma
// organização, por isso escolhe-se aqui o projeto em vez de no Grafana.
export default async function MetricsPage() {
  return <MetricsClient platformAdmin={await isPlatformAdmin()} />;
}
