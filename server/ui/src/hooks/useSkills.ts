import { useCallback, useEffect, useRef, useState } from "react";
import type { ServerMessage } from "../types/protocol";

export interface Skill {
	name: string;
	description?: string;
	input_hint?: string;
}

type Subscribe = (handler: (message: ServerMessage) => void) => () => void;

export function useSkills(
	sessionId?: string,
	subscribe?: Subscribe,
	active = true,
): Skill[] {
	const [skills, setSkills] = useState<Skill[]>([]);
	const generationRef = useRef(0);

	const load = useCallback(() => {
		const generation = ++generationRef.current;
		if (!active) return;
		const url = sessionId
			? `/api/skills?session=${encodeURIComponent(sessionId)}`
			: "/api/skills";
		fetch(url)
			.then((response) => (response.ok ? response.json() : []))
			.then((data: Skill[]) => {
				if (generationRef.current === generation) setSkills(data ?? []);
			})
			.catch(() => {
				if (generationRef.current === generation) setSkills([]);
			});
	}, [active, sessionId]);

	useEffect(() => {
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			if (message.type === "skills_changed") load();
		});
	}, [load, subscribe]);

	return skills;
}
