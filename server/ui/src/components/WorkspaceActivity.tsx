import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import type { ManagedToolsStatus } from "../api/capabilities";
import { getLSPActivity } from "../api/lsp";
import { queryKeys } from "../api/query";
import { ActivityCenter, type ActivityItem } from "./ActivityCenter";
import {
	languageServerActivity,
	managedToolActivities,
	managedToolErrorKey,
} from "./workspaceActivitySources";

interface Props {
	hasLSP: boolean;
	tools?: ManagedToolsStatus;
	activities?: readonly ActivityItem[];
}

const NO_ACTIVITIES: readonly ActivityItem[] = [];

// This adapter gathers the first two activity sources. The core activity
// center remains independent, and unrelated sources can provide normalized
// items through activities.
export function WorkspaceActivity({
	hasLSP,
	tools,
	activities = NO_ACTIVITIES,
}: Props) {
	const [dismissedError, setDismissedError] = useState("");
	const query = useQuery({
		queryKey: queryKeys.lsp.activity,
		queryFn: ({ signal }) => getLSPActivity(signal),
		enabled: hasLSP,
		refetchInterval: (current) => (current.state.data?.analyzing ? 1000 : 4000),
	});
	const services = query.data?.services;
	const toolErrorKey = managedToolErrorKey(tools);
	const showToolError = !!toolErrorKey && dismissedError !== toolErrorKey;
	const items = useMemo(
		() => [
			...activities,
			...managedToolActivities(tools, showToolError),
			...(services ?? []).map(languageServerActivity),
		],
		[activities, services, showToolError, tools],
	);

	return (
		<ActivityCenter
			items={items}
			onDismiss={(id) => {
				if (id === "managed-tools:error") setDismissedError(toolErrorKey);
			}}
		/>
	);
}
