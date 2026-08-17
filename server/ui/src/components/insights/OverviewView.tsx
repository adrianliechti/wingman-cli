import { ChevronRight } from "lucide-react";
import type { GraphNode, GraphOverview } from "../../api/insights";
import { nodeTargetLine } from "./nodes";
import { NodeRow } from "./shared";

function StatTile({ label, value }: { label: string; value: number }) {
	return (
		<div className="rounded-md border border-border-subtle bg-bg-surface/10 px-3 py-2">
			<div className="text-[18px] font-medium text-fg tabular-nums">
				{value.toLocaleString()}
			</div>
			<div className="text-[10px] uppercase tracking-wider text-fg-dim">
				{label}
			</div>
		</div>
	);
}

function Card({
	title,
	children,
}: {
	title: string;
	children: React.ReactNode;
}) {
	return (
		<section className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border-subtle bg-bg-surface/10">
			<div className="flex shrink-0 items-center border-b border-border-subtle px-2.5 py-1.5">
				<span className="text-[10px] font-medium uppercase tracking-wider text-fg-dim">
					{title}
				</span>
			</div>
			<div className="max-h-80 min-h-0 flex-1 overflow-y-auto @3xl:max-h-none">
				{children}
			</div>
		</section>
	);
}

export function OverviewView({
	overview,
	onOpenFile,
	onExplore,
	onSelectModule,
}: {
	overview: GraphOverview;
	onOpenFile: (path: string, line?: number) => void;
	onExplore: (node: GraphNode) => void;
	onSelectModule: (path: string) => void;
}) {
	const { arch } = overview;
	const languages = arch.languages ?? [];
	const modules = arch.modules ?? [];
	const hotspots = arch.hotspots ?? [];
	const entryPoints = arch.entry_points ?? [];
	const maxLangNodes = Math.max(1, ...languages.map((l) => l.nodes));

	const openNode = (node: GraphNode) =>
		onOpenFile(node.file, nodeTargetLine(node));

	return (
		<div className="@container flex h-full min-h-0 flex-col gap-3 p-3">
			<div className="grid shrink-0 grid-cols-2 gap-2 @2xl:grid-cols-4">
				<StatTile label="Files" value={arch.total_files} />
				<StatTile label="Symbols" value={arch.total_nodes} />
				<StatTile label="Relations" value={arch.total_edges} />
				<StatTile label="Languages" value={languages.length} />
			</div>
			<div className="grid min-h-0 flex-1 grid-cols-1 gap-3 overflow-y-auto @3xl:grid-cols-2 @3xl:grid-rows-2 @3xl:overflow-visible">
				<Card title="Largest modules">
					{modules.map((module) => (
						<button
							key={module.path}
							type="button"
							title={`Show ${module.path} on the module map`}
							onClick={() => onSelectModule(module.path)}
							className="group flex w-full items-center gap-1.5 border-b border-border-subtle/60 px-2.5 py-1 text-left text-[11px] last:border-b-0 hover:bg-bg-hover"
						>
							<span className="min-w-0 flex-1 truncate font-mono text-fg-muted group-hover:text-fg">
								{module.path}
							</span>
							<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
								{module.files} files · {module.nodes} symbols
							</span>
							<ChevronRight
								size={11}
								className="shrink-0 text-fg-dim opacity-0 group-hover:opacity-100"
							/>
						</button>
					))}
				</Card>
				<Card title="Hotspots">
					{hotspots.map((hotspot) => (
						<NodeRow
							key={hotspot.node.id}
							node={hotspot.node}
							detail={`${hotspot.callers} in · ${hotspot.callees} out`}
							onOpen={openNode}
							onExplore={onExplore}
						/>
					))}
					{hotspots.length === 0 && (
						<div className="px-3 py-4 text-center text-[11px] text-fg-dim">
							No call relations indexed yet
						</div>
					)}
				</Card>
				<Card title="Languages">
					{languages.map((lang) => (
						<div
							key={lang.lang}
							className="border-b border-border-subtle/60 px-2.5 py-1.5 last:border-b-0"
						>
							<div className="flex items-baseline justify-between gap-2 text-[11px]">
								<span className="truncate text-fg-muted">{lang.lang}</span>
								<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
									{lang.files.toLocaleString()} files ·{" "}
									{lang.nodes.toLocaleString()} symbols
								</span>
							</div>
							<div className="mt-1 h-1 overflow-hidden rounded-full bg-bg-active">
								<div
									className="h-full rounded-full bg-accent/60"
									style={{
										width: `${Math.max(2, (lang.nodes / maxLangNodes) * 100)}%`,
									}}
								/>
							</div>
						</div>
					))}
				</Card>
				<Card title="Entry points">
					{entryPoints.map((node) => (
						<NodeRow
							key={node.id}
							node={node}
							onOpen={openNode}
							onExplore={onExplore}
						/>
					))}
					{entryPoints.length === 0 && (
						<div className="px-3 py-4 text-center text-[11px] text-fg-dim">
							No main functions found
						</div>
					)}
				</Card>
			</div>
		</div>
	);
}
