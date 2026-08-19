import { useCallback, useEffect, useState } from "react";
import type { Capabilities } from "../api/capabilities";
import type { ServerMessage } from "../types/protocol";

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
