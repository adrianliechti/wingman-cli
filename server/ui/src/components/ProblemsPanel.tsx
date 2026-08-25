import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	AlertCircle,
	AlertTriangle,
	CheckCircle2,
	FileCode2,
	Info,
	Loader2,
} from "lucide-react";
import { useEffect, useMemo } from "react";
import { getWorkspaceDiagnostics } from "../api/lsp";
import { queryKeys } from "../api/query";
import type { DiagnosticEntry } from "../types/protocol";
import { PanelEmptyState } from "./ui/EmptyState";

interface Props {
	onOpenFile: (path: string, line: number, column?: number) => void;
	refreshKey?: number;
}

interface DiagnosticGroup {
	path: string;
	fileName: string;
	directory: string;
	diagnostics: DiagnosticEntry[];
	errors: number;
	warnings: number;
}

const EMPTY_DIAGNOSTICS: DiagnosticEntry[] = [];

function groupDiagnostics(diagnostics: DiagnosticEntry[]): DiagnosticGroup[] {
	const groups = new Map<string, DiagnosticGroup>();
	for (const diagnostic of diagnostics) {
		let group = groups.get(diagnostic.path);
		if (!group) {
			const parts = diagnostic.path.split("/");
			const fileName = parts.pop() || diagnostic.path;
			group = {
				path: diagnostic.path,
				fileName,
				directory: parts.join("/"),
				diagnostics: [],
				errors: 0,
				warnings: 0,
			};
			groups.set(diagnostic.path, group);
		}
		group.diagnostics.push(diagnostic);
		if (diagnostic.severity === "error") group.errors++;
		else if (diagnostic.severity === "warning") group.warnings++;
	}

	return Array.from(groups.values()).sort(
		(a, b) =>
			b.errors - a.errors ||
			b.warnings - a.warnings ||
			a.path.localeCompare(b.path),
	);
}

function SeverityIcon({ severity }: { severity: DiagnosticEntry["severity"] }) {
	switch (severity) {
		case "error":
			return <AlertCircle size={12} className="shrink-0 text-danger/70" />;
		case "warning":
			return <AlertTriangle size={12} className="shrink-0 text-warning/70" />;
		default:
			return <Info size={12} className="shrink-0 text-fg-dim" />;
	}
}

export function ProblemsPanel({ onOpenFile, refreshKey = 0 }: Props) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: queryKeys.diagnostics.workspace,
		queryFn: ({ signal }) => getWorkspaceDiagnostics(signal),
		refetchInterval: (current) =>
			current.state.data?.analyzing ? 3000 : false,
	});
	const coverage = query.data ?? null;
	const diagnostics = coverage?.diagnostics ?? EMPTY_DIAGNOSTICS;
	const loading = query.isPending || query.isFetching;
	const error = query.error ? "Refresh failed" : null;
	const groups = useMemo(() => groupDiagnostics(diagnostics), [diagnostics]);

	useEffect(() => {
		if (refreshKey === 0) return;
		void queryClient.invalidateQueries({
			queryKey: queryKeys.diagnostics.workspace,
		});
	}, [queryClient, refreshKey]);

	return (
		<div className="flex h-full flex-col overflow-hidden bg-transparent">
			<div className="flex flex-1 flex-col overflow-y-auto pb-1.5">
				{error && (
					<div className="mx-3 my-1.5 shrink-0 px-2 py-1.5 rounded bg-danger/5 text-[10px] text-danger/80">
						{error}
						{coverage ? "; showing the last successful result." : "."}
					</div>
				)}
				{diagnostics.length === 0 &&
					!loading &&
					(error && !coverage ? (
						<PanelEmptyState
							icon={AlertTriangle}
							iconClassName="text-warning/70"
							title="Diagnostics unavailable"
							hint="Refresh to check your workspace again."
						/>
					) : (
						<PanelEmptyState
							icon={CheckCircle2}
							iconClassName="text-success/70"
							title="No problems detected"
							hint="Errors and warnings from your language servers appear here."
						/>
					))}
				{diagnostics.length === 0 && loading && (
					<PanelEmptyState
						icon={Loader2}
						iconClassName="animate-spin text-fg-dim"
						title="Checking problems…"
					/>
				)}
				{groups.map((group) => {
					const infos =
						group.diagnostics.length - group.errors - group.warnings;
					return (
						<section
							key={group.path}
							data-problem-file={group.path}
							className="mb-1"
						>
							<div
								title={group.path}
								className="sticky top-0 z-10 flex items-center gap-1.5 border-b border-border-subtle/60 bg-bg-surface/80 px-3 py-1.5 backdrop-blur-md"
							>
								<FileCode2 size={12} className="shrink-0 text-fg-dim" />
								<span className="flex min-w-0 flex-1 items-baseline gap-1.5 overflow-hidden">
									<span className="truncate font-mono text-[11px] text-fg-muted">
										{group.fileName}
									</span>
									{group.directory && (
										<span className="truncate font-mono text-[9px] text-fg-dim">
											{group.directory}
										</span>
									)}
								</span>
								{group.errors > 0 && (
									<span className="flex shrink-0 items-center gap-0.5 text-[10px] tabular-nums text-danger/80">
										<AlertCircle size={10} />
										{group.errors}
									</span>
								)}
								{group.warnings > 0 && (
									<span className="flex shrink-0 items-center gap-0.5 text-[10px] tabular-nums text-warning/80">
										<AlertTriangle size={10} />
										{group.warnings}
									</span>
								)}
								{infos > 0 && (
									<span className="flex shrink-0 items-center gap-0.5 text-[10px] tabular-nums text-fg-dim">
										<Info size={10} />
										{infos}
									</span>
								)}
							</div>
							<div data-problem-list className="py-0.5">
								{group.diagnostics.map((diagnostic, index) => (
									<button
										type="button"
										key={`${diagnostic.line}:${diagnostic.column}:${index}`}
										data-problem-entry
										title={`${diagnostic.message} · ${diagnostic.path}:${diagnostic.line}:${diagnostic.column}${diagnostic.source ? ` · ${diagnostic.source}` : ""}`}
										className="flex w-full cursor-pointer items-center gap-1.5 px-3 py-1 text-left text-[11px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
										onClick={() =>
											onOpenFile(
												diagnostic.path,
												diagnostic.line,
												diagnostic.column,
											)
										}
									>
										<SeverityIcon severity={diagnostic.severity} />
										<span className="min-w-0 flex-1 truncate">
											{diagnostic.message}
										</span>
									</button>
								))}
							</div>
						</section>
					);
				})}
			</div>
		</div>
	);
}
