import { ArrowLeft, Menu, SquarePen, X } from "lucide-react";
import { useCallback, useState } from "react";
import { useWorkspace } from "../state/workspaceContext.ts";
import { workspaceClient } from "../state/workspaceClient.ts";
import { formatAgentName } from "../utils/agents";
import { AgentSessions } from "./AgentSessions";
import { Dialog } from "./ui/Feedback";

interface Props {
	title: string;
	connected: boolean;
	running: boolean;
	currentSessionId: string;
	runningSessionIds: Set<string>;
	onSelectBackend: (id: string) => void;
	onSelectSession: (id: string) => void;
	onDeleteSession: (id: string, title: string) => void;
	onNewSession: () => void;
	onBackToChat?: () => void;
}

// Navigation only: the existing workspace keeps rendering the chat/editor.
// Native agent selection avoids a popup nested behind a modal or its backdrop.
export function MobileNavigation(props: Props) {
	const { backend } = useWorkspace();
	const backends = workspaceClient().scope.backends;
	const [open, setOpen] = useState(false);
	const close = useCallback(() => setOpen(false), []);
	return (
		<>
			<header
				data-mobile-navigation
				className="shrink-0 border-b border-border-subtle bg-bg"
			>
				<div className="flex h-14 items-center gap-2 px-2">
					<button
						type="button"
						className="flex size-11 shrink-0 items-center justify-center rounded-lg text-fg-muted active:bg-bg-hover"
						aria-label="Show sessions"
						onClick={() => setOpen(true)}
					>
						<Menu size={21} />
					</button>
					<select
						aria-label="Agent"
						value={backend}
						onChange={(event) => props.onSelectBackend(event.target.value)}
						className="h-11 min-w-0 flex-1 rounded-lg bg-bg-surface px-3 text-base font-medium text-fg"
					>
						{backends.map((agent) => (
							<option key={agent.id} value={agent.id}>
								{formatAgentName(agent.id, agent.name)}
							</option>
						))}
					</select>
					<button
						type="button"
						className="flex size-11 shrink-0 items-center justify-center rounded-lg text-fg-muted active:bg-bg-hover"
						aria-label="New session"
						onClick={props.onNewSession}
					>
						<SquarePen size={21} />
					</button>
				</div>
				<div className="flex min-w-0 items-center gap-2 px-4 pb-2 text-xs text-fg-dim">
					{props.onBackToChat && (
						<button
							type="button"
							className="flex min-h-11 items-center gap-1 text-fg-muted"
							onClick={props.onBackToChat}
							aria-label="Back to chat"
						>
							<ArrowLeft size={18} /> Chat
						</button>
					)}
					<span className="min-w-0 flex-1 truncate">{props.title}</span>
					<span
						role="status"
						className={props.connected ? "text-fg-muted" : "text-warning"}
					>
						{!props.connected
							? "Reconnecting…"
							: props.running
								? "Working…"
								: "Ready"}
					</span>
				</div>
			</header>
			<Dialog open={open} title="Sessions" onClose={close} initialFocus="first">
				<div
					data-mobile-sessions
					className="h-[60dvh] min-h-0 w-full overflow-hidden"
				>
					<AgentSessions
						touch
						currentSessionId={props.currentSessionId}
						runningSessionIds={props.runningSessionIds}
						onSessionSelect={(id) => {
							close();
							props.onSelectSession(id);
						}}
						onSessionDelete={(id, title) => {
							close();
							props.onDeleteSession(id, title);
						}}
					/>
				</div>
				<button
					type="button"
					className="flex min-h-11 items-center gap-2 rounded-lg bg-bg-hover px-4 text-sm text-fg"
					onClick={close}
				>
					<X size={18} /> Close sessions
				</button>
			</Dialog>
		</>
	);
}
