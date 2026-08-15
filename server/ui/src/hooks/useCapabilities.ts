import { useCallback, useEffect, useState } from "react";
import type { ServerMessage } from "../types/protocol";

interface Capabilities {
	git: boolean;
	git_init?: boolean;
	lsp: boolean;
	diffs: boolean;
	tasks?: boolean;
	terminal?: boolean;
	platform?: string;
	workspace_name?: string;
	notice?: string;
}

type Subscribe = (handler: (msg: ServerMessage) => void) => () => void;

export function useCapabilities(subscribe?: Subscribe): Capabilities | null {
	const [caps, setCaps] = useState<Capabilities | null>(null);

	const load = useCallback(async () => {
		try {
			const res = await fetch("/api/capabilities");
			if (!res.ok) return;
			const data: Capabilities = await res.json();
			setCaps(data);
		} catch {}
	}, []);

	useEffect(() => {
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "capabilities_changed" || msg.type === "agent_changed") {
				load();
			}
		});
	}, [subscribe, load]);

	return caps;
}
