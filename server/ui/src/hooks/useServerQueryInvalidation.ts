import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import {
	invalidateAllServerQueries,
	invalidateForServerMessage,
} from "../api/query";
import type { ServerMessage } from "../types/protocol";

type Subscribe = (handler: (message: ServerMessage) => void) => () => void;

export function useServerQueryInvalidation(
	subscribe: Subscribe | undefined,
	connected: boolean,
): void {
	const queryClient = useQueryClient();
	const connectedOnce = useRef(false);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			invalidateForServerMessage(queryClient, message);
		});
	}, [queryClient, subscribe]);

	useEffect(() => {
		if (!connected) return;
		if (!connectedOnce.current) {
			connectedOnce.current = true;
			return;
		}
		invalidateAllServerQueries(queryClient);
	}, [connected, queryClient]);
}
