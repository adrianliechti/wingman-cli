import { AlertCircle, AlertTriangle, Info, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type {
	DiagnosticEntry,
	ServerMessage,
	WorkspaceDiagnostics,
} from "../types/protocol";

interface Props {
	onOpenFile: (path: string, line: number, column?: number) => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function ProblemsPanel({ onOpenFile, subscribe }: Props) {
	const [diagnostics, setDiagnostics] = useState<DiagnosticEntry[]>([]);
	const [coverage, setCoverage] = useState<WorkspaceDiagnostics | null>(null);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const requestRef = useRef<AbortController | null>(null);
	const refreshTimerRef = useRef<number | null>(null);

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

	const SeverityIcon = ({ severity }: { severity: string }) => {
		switch (severity) {
			case "error":
				return <AlertCircle size={12} className="text-danger/70 shrink-0" />;
			case "warning":
				return <AlertTriangle size={12} className="text-warning/70 shrink-0" />;
			default:
				return <Info size={12} className="text-fg-dim shrink-0" />;
		}
	};

	const coverageTotal = coverage
		? `${coverage.discovered_files}${coverage.discovery_truncated ? "+" : ""}`
		: "";
	const incomplete =
		coverage !== null &&
		(coverage.checked_files < coverage.discovered_files ||
			coverage.discovery_truncated ||
			coverage.unknown_files > 0 ||
			coverage.unavailable_servers.length > 0);

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg">
			<div className="h-8 px-3 flex items-center gap-2 shrink-0">
				<span className="text-[11px] text-fg-muted">Diagnostics</span>
				<span className="text-[10px] text-fg-dim tabular-nums">
					{diagnostics.length}
				</span>
				{coverage && (
					<span
						className="text-[10px] text-fg-dim tabular-nums truncate"
						title={`${coverage.checked_files} of ${coverageTotal} source files checked`}
					>
						{coverage.checked_files}/{coverageTotal} files
					</span>
				)}
				<button
					type="button"
					disabled={loading}
					onClick={() => void load()}
					title="Refresh diagnostics"
					aria-label="Refresh diagnostics"
					className="ml-auto w-6 h-6 flex items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg-hover disabled:opacity-50"
				>
					<RefreshCw size={11} className={loading ? "animate-spin" : ""} />
				</button>
			</div>
			<div className="overflow-y-auto flex-1 px-1 pb-2">
				{error && (
					<div className="mx-2 mb-1 px-2 py-1.5 rounded bg-danger/5 text-[10px] text-danger/80">
						{error}
						{coverage ? "; showing the last successful result." : "."}
					</div>
				)}
				{incomplete && coverage && (
					<div className="mx-2 mb-1 px-2 py-1.5 rounded bg-warning/5 text-[10px] text-fg-dim">
						Results are partial
						{coverage.unknown_files > 0
							? ` · ${coverage.unknown_files} files returned no data`
							: ""}
						{coverage.unavailable_servers.length > 0
							? ` · unavailable: ${coverage.unavailable_servers.join(", ")}`
							: ""}
					</div>
				)}
				{diagnostics.length === 0 && !loading && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						{error && !coverage
							? "Diagnostics unavailable"
							: incomplete
								? "No reported problems"
								: "No problems detected"}
					</div>
				)}
				{diagnostics.length === 0 && loading && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						Checking problems…
					</div>
				)}
				{diagnostics.map((d, i) => {
					const fileName = d.path.split("/").pop() || d.path;
					return (
						<div
							key={`${d.path}:${d.line}:${d.column}:${i}`}
							className="flex items-start gap-1.5 mx-1 px-2 py-1 rounded cursor-pointer text-[11px] text-fg-muted hover:bg-bg-hover hover:text-fg transition-colors"
							onClick={() => onOpenFile(d.path, d.line, d.column)}
						>
							<SeverityIcon severity={d.severity} />
							<div className="min-w-0 flex-1">
								<div className="truncate">{d.message}</div>
								<div className="text-[10px] text-fg-dim font-mono">
									{fileName}:{d.line}
								</div>
							</div>
						</div>
					);
				})}
			</div>
		</div>
	);
}
