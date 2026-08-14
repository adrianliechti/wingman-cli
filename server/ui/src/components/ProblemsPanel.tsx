import {
	AlertCircle,
	AlertTriangle,
	FileCode2,
	Info,
	Loader2,
	RefreshCw,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type {
	DiagnosticEntry,
	ServerMessage,
	WorkspaceDiagnostics,
} from "../types/protocol";

interface Props {
	onOpenFile: (path: string, line: number, column?: number) => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

interface DiagnosticGroup {
	path: string;
	fileName: string;
	directory: string;
	diagnostics: DiagnosticEntry[];
	errors: number;
	warnings: number;
}

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

export function ProblemsPanel({ onOpenFile, subscribe }: Props) {
	const [diagnostics, setDiagnostics] = useState<DiagnosticEntry[]>([]);
	const [coverage, setCoverage] = useState<WorkspaceDiagnostics | null>(null);
	const [loading, setLoading] = useState(true);
	const [error, setError] = useState<string | null>(null);
	const requestRef = useRef<AbortController | null>(null);
	const refreshTimerRef = useRef<number | null>(null);
	const groups = useMemo(() => groupDiagnostics(diagnostics), [diagnostics]);

	const load = useCallback(async () => {
		if (refreshTimerRef.current !== null) {
			window.clearTimeout(refreshTimerRef.current);
			refreshTimerRef.current = null;
		}
		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		setLoading(true);
		setError(null);
		try {
			const res = await fetch("/api/lsp/diagnostics", {
				signal: controller.signal,
			});
			if (controller.signal.aborted) return;
			if (!res.ok) {
				setError(`Refresh failed (${res.status})`);
				return;
			}
			const data = (await res.json()) as WorkspaceDiagnostics;
			if (controller.signal.aborted) return;
			setCoverage(data);
			setDiagnostics(data.diagnostics);
			if (data.analyzing) {
				refreshTimerRef.current = window.setTimeout(() => {
					refreshTimerRef.current = null;
					void load();
				}, 3000);
			}
		} catch (requestError) {
			if (
				requestRef.current === controller &&
				!(
					requestError instanceof DOMException &&
					requestError.name === "AbortError"
				)
			) {
				setError("Refresh failed");
			}
		} finally {
			if (requestRef.current === controller) {
				requestRef.current = null;
				setLoading(false);
			}
		}
	}, []);

	useEffect(() => {
		void load();
		return () => {
			requestRef.current?.abort();
			if (refreshTimerRef.current !== null) {
				window.clearTimeout(refreshTimerRef.current);
			}
		};
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		const unsubscribe = subscribe((msg) => {
			if (msg.type === "diagnostics_changed") {
				if (refreshTimerRef.current !== null) {
					window.clearTimeout(refreshTimerRef.current);
				}
				refreshTimerRef.current = window.setTimeout(() => {
					refreshTimerRef.current = null;
					void load();
				}, 250);
			}
		});
		return () => {
			unsubscribe();
			if (refreshTimerRef.current !== null) {
				window.clearTimeout(refreshTimerRef.current);
				refreshTimerRef.current = null;
			}
		};
	}, [subscribe, load]);

	const coverageTitle = coverage
		? [
				`${diagnostics.length} ${diagnostics.length === 1 ? "problem" : "problems"} in ${groups.length} ${groups.length === 1 ? "file" : "files"}`,
				`${coverage.checked_files} source files checked`,
				...(coverage.unavailable_servers.length > 0
					? [`unavailable: ${coverage.unavailable_servers.join(", ")}`]
					: []),
			].join(" · ")
		: "";

	return (
		<div className="flex h-full flex-col overflow-hidden bg-transparent">
			<div className="h-9 px-3 flex items-center gap-2 shrink-0 border-b border-border-subtle bg-bg-surface/20">
				<span className="text-[11px] text-fg-muted">Diagnostics</span>
				{diagnostics.length > 0 && (
					<span
						className="min-w-4 h-4 px-1 rounded-full bg-bg-active text-[9px] leading-4 text-center text-fg-dim tabular-nums"
						title={coverageTitle}
					>
						{diagnostics.length}
					</span>
				)}
				<div className="flex-1" />
				<button
					type="button"
					disabled={loading}
					onClick={() => void load()}
					title="Refresh diagnostics"
					aria-label="Refresh diagnostics"
					className="w-6 h-6 flex items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg-hover disabled:opacity-50"
				>
					{loading || coverage?.analyzing ? (
						<Loader2 size={11} className="animate-spin" />
					) : (
						<RefreshCw size={11} />
					)}
				</button>
			</div>
			<div className="flex-1 overflow-y-auto px-1 py-1.5">
				{error && (
					<div className="mx-2 mb-1 px-2 py-1.5 rounded bg-danger/5 text-[10px] text-danger/80">
						{error}
						{coverage ? "; showing the last successful result." : "."}
					</div>
				)}
				{diagnostics.length === 0 && !loading && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						{error && !coverage
							? "Diagnostics unavailable"
							: "No problems detected"}
					</div>
				)}
				{diagnostics.length === 0 && loading && (
					<div className="flex min-h-12 items-center justify-center gap-1.5 px-3 text-center text-[11px] text-fg-dim">
						<Loader2 size={11} className="animate-spin" />
						<span>Checking problems…</span>
					</div>
				)}
				{groups.map((group) => {
					return (
						<section
							key={group.path}
							data-problem-file={group.path}
							className="mx-1 mb-1 overflow-hidden rounded-md border border-border-subtle bg-bg-surface/10"
						>
							<div
								title={group.path}
								className="flex w-full items-center gap-1.5 px-2 py-1.5 text-left"
							>
								<FileCode2 size={12} className="shrink-0 text-fg-dim" />
								<span className="min-w-0 flex-1">
									<span className="block truncate text-[11px] text-fg-muted">
										{group.fileName}
									</span>
									{group.directory && (
										<span className="block truncate font-mono text-[9px] text-fg-dim">
											{group.directory}
										</span>
									)}
								</span>
							</div>
							<div data-problem-list>
								{group.diagnostics.map((diagnostic, index) => (
									<button
										type="button"
										key={`${diagnostic.line}:${diagnostic.column}:${index}`}
										data-problem-entry
										title={`${diagnostic.message} · ${diagnostic.path}:${diagnostic.line}:${diagnostic.column}${diagnostic.source ? ` · ${diagnostic.source}` : ""}`}
										className="flex w-full cursor-pointer items-center gap-1.5 border-b border-border-subtle/60 px-2 py-1 text-left text-[11px] text-fg-muted transition-colors last:border-b-0 hover:bg-bg-hover hover:text-fg"
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
