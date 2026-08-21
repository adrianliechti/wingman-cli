import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bot, ChevronDown, Loader2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { agentQueries, setCurrentAgent, type AgentInfo } from "../api/agents";
import { queryKeys } from "../api/query";
import { formatAgentName } from "../utils/agents";
import { useToast } from "./ui/Feedback";
import { FloatingMenu } from "./ui/Floating";

export const BUILTIN_AGENT_ID = "wingman";
const EMPTY_AGENTS: AgentInfo[] = [];

interface Props {
	onSwitchingChange?: (target: string | null) => void;
}

export function AgentPicker({ onSwitchingChange }: Props) {
	const toast = useToast();
	const queryClient = useQueryClient();
	const agents = useQuery(agentQueries.list()).data ?? EMPTY_AGENTS;
	const current =
		useQuery(agentQueries.current()).data?.agent || BUILTIN_AGENT_ID;
	const [open, setOpen] = useState(false);
	const [switching, setSwitching] = useState<string | null>(null);
	const btnRef = useRef<HTMLButtonElement>(null);
	const switchAgent = useMutation({ mutationFn: setCurrentAgent });

	useEffect(() => {
		onSwitchingChange?.(switching);
	}, [switching, onSwitchingChange]);

	const toggleOpen = useCallback(() => {
		if (switching) return;
		setOpen((value) => !value);
	}, [switching]);

	const select = useCallback(
		async (id: string) => {
			if (id === current || switching) return;
			setSwitching(id);
			setOpen(false);
			try {
				await switchAgent.mutateAsync(id);
				await queryClient.invalidateQueries({
					queryKey: queryKeys.agents.current,
					exact: true,
				});
			} catch (e) {
				const msg = e instanceof Error ? e.message : String(e);
				toast({
					title: "Could not switch agent",
					description: msg,
					tone: "error",
				});
				console.error("agent switch failed", e);
			} finally {
				setSwitching(null);
			}
		},
		[current, queryClient, switchAgent, switching, toast],
	);

	const displayedName = useMemo(() => {
		const id = switching ?? current;
		const match = agents.find((a) => a.id === id);
		return formatAgentName(id, match?.name);
	}, [agents, current, switching]);

	if (agents.length <= 1) return null;

	return (
		<div className="relative min-w-0">
			<button
				ref={btnRef}
				type="button"
				onClick={toggleOpen}
				disabled={!!switching}
				className="flex h-7 min-w-0 max-w-[180px] cursor-pointer items-center gap-1 rounded px-2 text-[11.5px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg disabled:cursor-wait disabled:opacity-70"
				title={`Agent: ${displayedName}`}
				aria-haspopup="menu"
				aria-expanded={open}
			>
				{switching ? (
					<Loader2 size={12} className="shrink-0 animate-spin" />
				) : (
					<Bot size={12} className="shrink-0" />
				)}
				<span className="truncate">{displayedName}</span>
				{!switching && (
					<ChevronDown size={10} className="shrink-0 text-fg-dim" />
				)}
			</button>
			<FloatingMenu
				open={open && !switching}
				onOpenChange={setOpen}
				reference={btnRef.current}
				placement="bottom-start"
				label="Agent"
				className="z-[100] min-w-[180px] max-w-[260px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl"
			>
				<div className="py-1 max-h-[260px] overflow-y-auto">
					{agents.map((a) => (
						<button
							type="button"
							role="menuitemradio"
							aria-checked={a.id === current}
							key={a.id}
							className={`block w-full text-left px-3 py-1.5 text-[12px] cursor-pointer whitespace-nowrap transition-colors ${
								a.id === current
									? "text-fg bg-bg-active"
									: "text-fg-muted hover:text-fg hover:bg-bg-hover"
							}`}
							onClick={() => select(a.id)}
						>
							{formatAgentName(a.id, a.name)}
						</button>
					))}
				</div>
			</FloatingMenu>
		</div>
	);
}
