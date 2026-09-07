import { Bot, ChevronDown } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { useWorkspace } from "../state/workspaceContext.ts";
import { workspaceClient } from "../state/workspaceClient.ts";
import { formatAgentName } from "../utils/agents";
import { FloatingMenu } from "./ui/Floating";

export const BUILTIN_AGENT_ID = "wingman";

interface Props {
	onSelect: (id: string) => void | Promise<void>;
	currentId?: string;
}
export function AgentPicker({ onSelect, currentId }: Props) {
	const { backend } = useWorkspace();
	const current = currentId ?? backend;
	const agents = workspaceClient().scope.backends;
	const [open, setOpen] = useState(false);
	const btnRef = useRef<HTMLButtonElement>(null);
	const toggleOpen = () => setOpen((value) => !value);
	const select = (id: string) => {
		setOpen(false);
		onSelect(id);
	};
	const displayedName = useMemo(() => {
		const id = current;
		const match = agents.find((a) => a.id === id);
		return formatAgentName(id, match?.name);
	}, [agents, current]);

	if (agents.length <= 1) return null;

	return (
		<div className="relative min-w-0">
			<button
				ref={btnRef}
				type="button"
				onClick={toggleOpen}
				className="flex h-7 min-w-0 max-w-[180px] cursor-pointer items-center gap-1 rounded px-2 text-[11.5px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg disabled:cursor-wait disabled:opacity-70"
				title={`Agent: ${displayedName}`}
				aria-haspopup="menu"
				aria-expanded={open}
			>
				<Bot size={12} className="shrink-0" />
				<span className="truncate">{displayedName}</span>
				<ChevronDown size={10} className="shrink-0 text-fg-dim" />
			</button>
			<FloatingMenu
				open={open}
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
