import { useQuery } from "@tanstack/react-query";
import { skillsQuery, type Skill } from "../api/skills";

export type { Skill } from "../api/skills";

export function useSkills(sessionId?: string, active = true): Skill[] {
	return (
		useQuery({
			...skillsQuery(sessionId),
			enabled: active,
		}).data ?? []
	);
}
