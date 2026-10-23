"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { api, projectPath, type ProjectDetail } from "@/lib/platformClient";

// Papel de quem está a ver, num projeto: "platform-admin" (infra-admin ou
// super-admin) ou o papel de membro. Serve só para esconder o que a pessoa
// não pode usar -- quem decide é sempre a API do launcher.
export type ProjectRole = "platform-admin" | "admin" | "developer" | "viewer" | "";

const rank: Record<ProjectRole, number> = { "": 0, viewer: 1, developer: 2, admin: 3, "platform-admin": 4 };

type Ctx = { role: ProjectRole; loaded: boolean };

const ProjectRoleContext = createContext<Ctx>({ role: "", loaded: false });

export function ProjectRoleProvider({ project, children }: { project: string; children: React.ReactNode }) {
  const [state, setState] = useState<Ctx>({ role: "", loaded: false });
  useEffect(() => {
    api<ProjectDetail>(projectPath(project))
      .then((d) => setState({ role: (d.myRole ?? "") as ProjectRole, loaded: true }))
      .catch(() => setState({ role: "", loaded: true }));
  }, [project]);
  return <ProjectRoleContext.Provider value={state}>{children}</ProjectRoleContext.Provider>;
}

// useCan diz se o papel de quem está a ver chega para min. Enquanto o
// papel não carregou, responde false (os botões aparecem a seguir).
export function useCan(min: Exclude<ProjectRole, "">): boolean {
  const { role } = useContext(ProjectRoleContext);
  return rank[role] >= rank[min];
}

export function useProjectRole(): ProjectRole {
  return useContext(ProjectRoleContext).role;
}
