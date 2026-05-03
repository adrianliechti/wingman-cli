import { useCallback, useEffect, useState } from "react";
import type { ServerMessage } from "../types/protocol";

interface Capabilities {
	git: boolean;
	lsp: boolean;
	diffs: boolean;
	notice?: string;
}

type Subscribe = (handler: (msg: ServerMessage) => void) => () => void;

// useCapabilities fetches the server's feature gate on mount and refetches
// whenever the server emits capabilities_changed (e.g. agent ran `git init`
// in a scratch dir, flipping the right-panel tabs on). Returns null while
// the first fetch is pending so panels don't flash.
export function useCapabilities(subscribe?: Subscribe): Capabilities | null {
	const [caps, setCaps] = useState<Capabilities | null>(null);

	const load = useCallback(async () => {
		try {
			const res = await fetch("/api/capabilities");
			if (!res.ok) return;
			const data: Capabilities = await res.json();
			setCaps(data);
		} catch {
			// network blip — keep last known caps, next event will refetch
		}
	}, []);

	useEffect(() => {
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "capabilities_changed") {
				load();
			}
		});
	}, [subscribe, load]);

	return caps;
}
