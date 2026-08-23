import type { ManagedToolsStatus } from "../api/capabilities";
import type { LSPServiceActivity } from "../types/protocol";
import type { ActivityItem } from "./ActivityCenter";

const LANGUAGE_ANALYSIS_HINT =
	"Language features may be incomplete while background analysis finishes.";

export function managedToolActivities(
	status: ManagedToolsStatus | undefined,
	showError: boolean,
): ActivityItem[] {
	if (status?.state === "installing") {
		const phase = status.phase ?? "checking";
		const label = status.label || "managed editor tools";
		const action =
			phase === "updating"
				? "Updating"
				: phase === "installing"
					? "Installing"
					: "Checking";
		const detail =
			phase === "updating"
				? "Downloading, verifying, and replacing the managed copy."
				: phase === "installing"
					? "Downloading, verifying, and installing the managed tool."
					: "Looking for a current project, system, or managed copy.";
		return [
			{
				id: `managed-tools:${status.tool || "all"}`,
				kind: "managed-tool",
				state: "running",
				label: `${action} ${label}`,
				detail,
				scope:
					status.current && status.total
						? `${status.current} / ${status.total}`
						: undefined,
				percentage:
					status.current && status.total
						? Math.round((status.current / status.total) * 100)
						: undefined,
				icon: "download",
			},
		];
	}
	if (status?.state !== "error" || !showError) return [];
	const unavailable = status.unavailable ?? [];
	return [
		{
			id: "managed-tools:error",
			kind: "managed-tool",
			state: "error",
			label: "Tool setup needs attention",
			detail:
				unavailable.length > 0
					? `Could not install ${unavailable.join(", ")}. Project and system tools still work.`
					: status.error ||
						"Automatic setup did not finish. Project and system tools still work.",
			dismissible: true,
		},
	];
}

export function managedToolErrorKey(status: ManagedToolsStatus | undefined) {
	if (status?.state !== "error") return "";
	return (
		(status.unavailable ?? []).join(",") || status.error || "managed-tools"
	);
}

export function languageServerActivity(
	service: LSPServiceActivity,
): ActivityItem {
	return {
		id: `language:${service.server}:${service.project}`,
		kind: "language-server",
		state: service.analyzing ? "running" : "ready",
		label: service.analyzing
			? `Indexing ${service.label}`
			: `${service.label} language server`,
		detail: service.analyzing ? undefined : "Ready",
		scope: service.project === "." ? "workspace" : service.project,
		operations: service.analyzing
			? service.operations.length > 0
				? service.operations.map((operation) => ({
						label: operation.title || "Analyzing project",
						detail: operation.message,
						percentage: operation.percentage,
					}))
				: [{ label: "Indexing project" }]
			: undefined,
		hint: service.analyzing ? LANGUAGE_ANALYSIS_HINT : undefined,
		icon: "code",
	};
}
