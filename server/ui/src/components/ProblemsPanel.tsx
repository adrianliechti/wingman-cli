import { AlertCircle, AlertTriangle, Info } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { DiagnosticEntry, ServerMessage } from "../types/protocol";

interface Props {
	onOpenFile: (path: string, line: number) => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function ProblemsPanel({ onOpenFile, subscribe }: Props) {
	const [diagnostics, setDiagnostics] = useState<DiagnosticEntry[]>([]);

	const load = useCallback(async () => {
		try {
			const res = await fetch("/api/diagnostics");
			if (!res.ok) {
				setDiagnostics([]);
				return;
			}
			const data: DiagnosticEntry[] = await res.json();
			setDiagnostics(data);
		} catch {
			setDiagnostics([]);
		}
	}, []);

	useEffect(() => {
		// eslint-disable-next-line react-hooks/set-state-in-effect -- standard data-load on mount
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "diagnostics_changed") {
				load();
			}
		});
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

	return (
		<div className="flex flex-col h-full overflow-hidden bg-bg">
			<div className="h-8 px-3 flex items-center shrink-0">
				<span className="text-[11px] text-fg-muted">Diagnostics</span>
			</div>
			<div className="overflow-y-auto flex-1 px-1 pb-2">
				{diagnostics.length === 0 && (
					<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
						No problems detected
					</div>
				)}
				{diagnostics.map((d, i) => {
					const fileName = d.path.split("/").pop() || d.path;
					return (
						<div
							key={`${d.path}:${d.line}:${d.column}:${i}`}
							className="flex items-start gap-1.5 mx-1 px-2 py-1 rounded cursor-pointer text-[11px] text-fg-muted hover:bg-bg-hover hover:text-fg transition-colors"
							onClick={() => onOpenFile(d.path, d.line)}
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
