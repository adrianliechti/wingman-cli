import { ExternalLink } from "lucide-react";
import type { GraphNode, GraphNodeKind } from "../../api/graph";
import { nodeLocation } from "./nodes";

const KIND_LETTER: Record<GraphNodeKind, string> = {
	function: "f",
	method: "m",
	constructor: "c",
	class: "C",
	interface: "I",
	type: "T",
	module: "M",
	constant: "k",
	variable: "v",
};

const KIND_CLASS: Record<GraphNodeKind, string> = {
	function: "bg-info/15 text-info",
	method: "bg-info/15 text-info",
	constructor: "bg-info/15 text-info",
	class: "bg-orange/15 text-orange",
	interface: "bg-purple/15 text-purple",
	type: "bg-purple/15 text-purple",
	module: "bg-warning/15 text-warning",
	constant: "bg-success/15 text-success",
	variable: "bg-success/15 text-success",
};

export function KindBadge({
	kind,
	size = 14,
}: {
	kind: GraphNodeKind;
	size?: number;
}) {
	return (
		<span
			title={kind}
			className={`grid shrink-0 place-items-center rounded font-mono leading-none ${KIND_CLASS[kind] ?? "bg-bg-active text-fg-dim"}`}
			style={{ width: size, height: size, fontSize: size - 5 }}
		>
			{KIND_LETTER[kind] ?? "?"}
		</span>
	);
}

export function NodeRow({
	node,
	detail,
	onOpen,
	onExplore,
}: {
	node: GraphNode;
	detail?: string;
	onOpen: (node: GraphNode) => void;
	onExplore?: (node: GraphNode) => void;
}) {
	const primary = onExplore ?? onOpen;
	return (
		<div className="group flex w-full items-center gap-1.5 border-b border-border-subtle/60 px-2 py-1 text-left text-[11px] last:border-b-0 hover:bg-bg-hover">
			<KindBadge kind={node.kind} />
			<button
				type="button"
				title={
					onExplore
						? `Explore the call graph of ${node.name}`
						: nodeLocation(node)
				}
				onClick={() => primary(node)}
				className="flex min-w-0 flex-1 cursor-pointer items-baseline gap-1.5 text-left"
			>
				<span className="truncate text-fg-muted group-hover:text-fg">
					{node.name}
				</span>
				<span className="min-w-0 truncate font-mono text-[9px] text-fg-dim">
					{node.file}
				</span>
			</button>
			{detail && (
				<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
					{detail}
				</span>
			)}
			{onExplore && (
				<button
					type="button"
					title={`Open ${nodeLocation(node)}`}
					aria-label={`Open ${nodeLocation(node)}`}
					onClick={() => onOpen(node)}
					className="invisible grid h-5 w-5 shrink-0 place-items-center rounded text-fg-dim group-hover:visible hover:bg-bg-active hover:text-fg"
				>
					<ExternalLink size={11} />
				</button>
			)}
		</div>
	);
}
