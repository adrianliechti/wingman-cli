import {
	createContext,
	useContext,
	useEffect,
	useSyncExternalStore,
} from "react";
import { useQuery } from "@tanstack/react-query";
import { fetchJSON } from "../api/http.ts";
import {
	EMPTY_SETTINGS,
	isDraft,
	splitSessionKey,
	type SessionSettings,
} from "./sessionStore.ts";
import { workspaceClient } from "./workspaceClient.ts";

export type SettingsPatch = Partial<
	Pick<SessionSettings, "model" | "effort" | "mode">
>;
type Navigation = {
	backend: string;
	selectBackend: (id: string) => void;
	drafts: Record<string, SettingsPatch>;
	setDraft: (key: string, patch: SettingsPatch) => void;
};
export const WorkspaceContext = createContext<Navigation | null>(null);
export function useWorkspace() {
	const value = useContext(WorkspaceContext);
	if (!value) throw new Error("Workspace provider missing");
	return value;
}
export function backendSettingsQuery(backend: string) {
	return {
		queryKey: ["server", "backend-settings", backend],
		queryFn: ({ signal }: { signal: AbortSignal }) =>
			fetchJSON<SessionSettings>(
				`/api/v2/backends/${encodeURIComponent(backend)}/settings`,
				{ signal },
			),
	};
}
export function useSessionSettings(key = "") {
	const client = workspaceClient();
	const { backend, drafts, setDraft } = useWorkspace();
	const draft = isDraft(key);
	const owner = key ? splitSessionKey(key).backendId : backend;
	const catalogs = useQuery({ ...backendSettingsQuery(owner), enabled: draft });
	const views = useSyncExternalStore(
		client.store.subscribe,
		client.store.getSnapshot,
	);
	useEffect(() => client.watch(key), [client, key]);
	const settings = draft
		? { ...(catalogs.data ?? EMPTY_SETTINGS), ...drafts[key] }
		: (views[key]?.settings ?? EMPTY_SETTINGS);
	const setSettings = async (patch: SettingsPatch) => {
		if (draft) setDraft(key, patch);
		else await client.command(key, { type: "settings", ...patch });
	};
	return { settings, setSettings };
}
