import { AlertTriangle, ChevronRight } from "lucide-react";
import { useState } from "react";
import type { GraphNode, GraphOverview } from "../../api/graph";
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
	count,
}: {
	title: string;
	count?: number;
	children: React.ReactNode;
}) {
	return (
		<section className="flex min-h-0 flex-col overflow-hidden rounded-md border border-border-subtle bg-bg-surface/10">
			<div className="flex shrink-0 items-center gap-2 border-b border-border-subtle px-2.5 py-1.5">
				<span className="text-[10px] font-medium uppercase tracking-wider text-fg-dim">
					{title}
				</span>
				{count !== undefined && count > 0 && (
					<span className="min-w-4 rounded-full bg-bg-active px-1 text-center text-[9px] leading-4 text-fg-dim tabular-nums">
						{count}
					</span>
				)}
			</div>
			<div className="min-h-0 overflow-y-auto">{children}</div>
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
	const [showSkipped, setShowSkipped] = useState(false);
	const { arch, status } = overview;
	const languages = arch.languages ?? [];
	const modules = arch.modules ?? [];
	const hotspots = arch.hotspots ?? [];
	const entryPoints = arch.entry_points ?? [];
	const skipped = (status.skipped ?? []).filter(
		(issue) =>
			issue.reason !== "no_tags_query" &&
			issue.reason !== "unsupported_language",
	);
	const maxLangNodes = Math.max(1, ...languages.map((l) => l.nodes));

	const openNode = (node: GraphNode) =>
		onOpenFile(node.file, nodeTargetLine(node));

	return (
		<div className="h-full overflow-y-auto p-3">
			<div className="mb-3 grid grid-cols-2 gap-2 sm:grid-cols-4">
				<StatTile label="Files" value={arch.total_files} />
				<StatTile label="Symbols" value={arch.total_nodes} />
				<StatTile label="Relations" value={arch.total_edges} />
				<StatTile label="Languages" value={languages.length} />
			</div>
			<div className="grid grid-cols-1 gap-3 lg:grid-cols-2 xl:grid-cols-3">
				<Card title="Languages" count={languages.length}>
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
				<Card title="Largest modules" count={modules.length}>
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
				<Card title="Hotspots" count={hotspots.length}>
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
				<Card title="Entry points" count={entryPoints.length}>
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
				{skipped.length > 0 && (
					<Card title="Not indexed" count={skipped.length}>
						<button
							type="button"
							onClick={() => setShowSkipped((value) => !value)}
							className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-[11px] text-fg-dim hover:bg-bg-hover"
						>
							<AlertTriangle size={11} className="shrink-0 text-warning/70" />
							<span className="flex-1">
								{skipped.length} {skipped.length === 1 ? "file" : "files"} could
								not be indexed
							</span>
							<ChevronRight
								size={11}
								className={`shrink-0 transition-transform ${showSkipped ? "rotate-90" : ""}`}
							/>
						</button>
						{showSkipped &&
							skipped.map((issue) => (
								<button
									key={issue.file}
									type="button"
									title={issue.reason}
									onClick={() => onOpenFile(issue.file)}
									className="flex w-full items-baseline gap-1.5 border-t border-border-subtle/60 px-2.5 py-1 text-left text-[11px] hover:bg-bg-hover"
								>
									<span className="min-w-0 flex-1 truncate font-mono text-fg-muted">
										{issue.file}
									</span>
									<span className="shrink-0 truncate text-[10px] text-fg-dim">
										{issue.reason}
									</span>
								</button>
							))}
					</Card>
				)}
			</div>
		</div>
	);
}
