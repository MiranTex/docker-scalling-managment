"use client";

import { useEffect, useState } from "react";
import type { DbStatus } from "@/lib/dbAdminClient";
import { formatBytes } from "./format";

export default function OverviewClient() {
  const [status, setStatus] = useState<DbStatus | null>(null);

  useEffect(() => {
    fetch("/api/admin/database").then(async (res) => {
      if (res.ok) setStatus(await res.json());
    });
  }, []);

  return (
    <div className="card wide">
      <h2>Estado</h2>
      {!status ? (
        <p className="muted">A carregar estado...</p>
      ) : (
        <table>
          <tbody>
            <tr>
              <th>Base de dados</th>
              <td>{status.postgres.database_name}</td>
            </tr>
            <tr>
              <th>Versão</th>
              <td>{status.postgres.version}</td>
            </tr>
            <tr>
              <th>Tamanho</th>
              <td>{formatBytes(status.postgres.database_size_bytes)}</td>
            </tr>
            <tr>
              <th>Ligações abertas</th>
              <td>{status.postgres.connection_count}</td>
            </tr>
            <tr>
              <th>A correr desde</th>
              <td>{new Date(status.postgres.start_time).toLocaleString("pt-PT")}</td>
            </tr>
            <tr>
              <th>Último backup</th>
              <td>
                {status.last_backup_at ? new Date(status.last_backup_at).toLocaleString("pt-PT") : "nenhum ainda"}
              </td>
            </tr>
          </tbody>
        </table>
      )}
    </div>
  );
}
