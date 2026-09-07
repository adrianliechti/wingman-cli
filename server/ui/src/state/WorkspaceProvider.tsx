import {
	useCallback,
	useEffect,
	useMemo,
	useState,
	useSyncExternalStore,
	type ReactNode,
} from "react";
import { WorkspaceContext, type SettingsPatch } from "./workspaceContext.ts";
import { workspaceClient } from "./workspaceClient.ts";

export function WorkspaceProvider({ children }: { children: ReactNode }) {
	const client = workspaceClient();
	const replaced = useSyncExternalStore(
		client.subscribeConnection,
		client.getReplaced,
	);
	const [backend, selectBackend] = useState(() => {
		const id = decodeURIComponent(
			location.pathname.split("/").filter(Boolean)[0] ?? "wingman",
		);
		return client.scope.backends.some((backend) => backend.id === id)
			? id
			: "wingman";
	});
	const [drafts, setDrafts] = useState<Record<string, SettingsPatch>>({});
	const setDraft = useCallback(
		(key: string, patch: SettingsPatch) =>
			setDrafts((current) => ({
				...current,
				[key]: { ...current[key], ...patch },
			})),
		[],
	);
	useEffect(() => {
		client.start();
		const focus = () => client.focus();
		window.addEventListener("focus", focus);
		return () => {
			window.removeEventListener("focus", focus);
			client.stop();
		};
	}, [client]);
	const value = useMemo(
		() => ({ backend, selectBackend, drafts, setDraft }),
		[backend, drafts, setDraft],
	);
	return (
		<WorkspaceContext.Provider value={value}>
			{children}
			{replaced && (
				<div
					role="alert"
					className="fixed bottom-4 left-1/2 z-[150] flex max-w-lg -translate-x-1/2 items-center gap-4 rounded-lg border border-border bg-bg-elevated p-4 text-sm text-fg shadow-xl"
				>
					<span>
						This workspace was reopened. Copy any unsaved edits before
						reloading.
					</span>
					<button
						type="button"
						className="cursor-pointer rounded border border-border px-3 py-1.5"
						onClick={() => location.assign("/")}
					>
						Reload workspace
					</button>
				</div>
			)}
		</WorkspaceContext.Provider>
	);
}
