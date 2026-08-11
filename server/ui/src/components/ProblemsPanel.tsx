import {
	AlertCircle,
	AlertTriangle,
	Info,
	Loader2,
	RefreshCw,
} from "lucide-react";
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
	const [loading, setLoading] = useState(true);
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

	const coverageTitle = coverage
		? [
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
				{diagnostics.map((d, i) => {
					const fileName = d.path.split("/").pop() || d.path;
					return (
						<button
							type="button"
							key={`${d.path}:${d.line}:${d.column}:${i}`}
							className="mx-1 flex w-[calc(100%-0.5rem)] cursor-pointer items-start gap-1.5 rounded px-2 py-1 text-left text-[11px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
							onClick={() => onOpenFile(d.path, d.line, d.column)}
						>
							<SeverityIcon severity={d.severity} />
							<div className="min-w-0 flex-1">
								<div className="truncate">{d.message}</div>
								<div className="text-[10px] text-fg-dim font-mono">
									{fileName}:{d.line}
								</div>
							</div>
						</button>
					);
				})}
			</div>
		</div>
	);
}
