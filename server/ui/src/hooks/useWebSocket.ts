import { useCallback, useMemo, useState, useSyncExternalStore } from "react";
import { workspaceClient } from "../state/workspaceClient.ts";
import type { PromptReply, TurnInputIntent } from "../types/protocol.ts";
import { useToast } from "../components/ui/Feedback.tsx";
export type {
	ChatEntry,
	PendingPrompt,
	PendingTurnInput,
} from "../types/session.ts";
export type { PromptReply } from "../types/protocol.ts";

// React bindings only. Transport, delivery receipts, and the ordered session
// store each have an owner outside the component lifecycle.
export function useWebSocket() {
	const client = workspaceClient();
	const views = useSyncExternalStore(
		client.store.subscribe,
		client.store.getSnapshot,
	);
	const connected = useSyncExternalStore(
		client.subscribeConnection,
		client.getConnected,
	);
	const toast = useToast();
	const [dismissed, setDismissed] = useState<Record<string, string | null>>({});
	const sessions = useMemo(
		() =>
			Object.fromEntries(
				Object.entries(views).map(([key, view]) => [
					key,
					{ ...view, error: dismissed[key] === view.error ? null : view.error },
				]),
			),
		[views, dismissed],
	);
	const run = useCallback(
		async (key: string, command: Parameters<typeof client.command>[1]) => {
			try {
				await client.command(key, command);
				return true;
			} catch (error) {
				toast({
					title: "Could not update session",
					description: String(error),
					tone: "error",
				});
				return false;
			}
		},
		[client, toast],
	);
	const actions = useMemo(
		() => ({
			subscribe: client.subscribe,
			observe: (keys: string[]) => {
				const releases = keys.map((key) => client.watch(key));
				return () => releases.forEach((release) => release());
			},
			sendChat: async (
				key: string,
				text: string,
				files?: string[],
				images?: string[],
				intent: TurnInputIntent = "follow_up",
			) => {
				try {
					await client.send(key, text, files, images, intent);
					return true;
				} catch (error) {
					toast({
						title: "Input delivery was not confirmed",
						description: `${String(error)}. Your input is kept; retrying uses the same request.`,
						tone: "error",
					});
					return false;
				}
			},
			cancel: (key: string, clearQueue = false) =>
				run(key, { type: "cancel", clearQueue }),
			removeQueued: (key: string, inputId: string) =>
				run(key, { type: "queue_remove", inputId }),
			updateQueued: (
				key: string,
				inputId: string,
				text: string,
				files?: string[],
				images?: string[],
			) => run(key, { type: "queue_update", inputId, text, files, images }),
			resumeQueue: (key: string) => run(key, { type: "queue_resume" }),
			clearQueue: (key: string) => run(key, { type: "queue_clear" }),
			dismissError: (key: string) =>
				setDismissed((current) => ({
					...current,
					[key]: client.store.getSnapshot()[key]?.error,
				})),
			respondPrompt: (key: string, promptId: string, reply: PromptReply) =>
				run(key, { type: "prompt_response", promptId, ...reply }),
		}),
		[client, run, toast],
	);
	return { ...actions, connected, sessions };
}
