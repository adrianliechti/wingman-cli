import { ChevronDown } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { Phase } from "../types/protocol";

interface Props {
	connected: boolean;
	phase: Phase;
	inputTokens: number;
	outputTokens: number;
}

export function StatusBar({
	connected,
	phase,
	inputTokens,
	outputTokens,
}: Props) {
	const [model, setModel] = useState("");
	const [models, setModels] = useState<string[]>([]);
	const [showPicker, setShowPicker] = useState(false);
	const pickerRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		fetch("/api/model")
			.then((r) => r.json())
			.then((data) => setModel(data.model || ""))
			.catch(() => {});
	}, []);

	const loadModels = useCallback(() => {
		fetch("/api/models")
			.then((r) => r.json())
			.then((data: Array<{ id: string }>) => setModels(data.map((m) => m.id)))
			.catch(() => {});
	}, []);

	const handlePickerToggle = useCallback(() => {
		if (!showPicker) loadModels();
		setShowPicker((v) => !v);
	}, [showPicker, loadModels]);

	const selectModel = useCallback((id: string) => {
		fetch("/api/model", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ model: id }),
		})
			.then((r) => r.json())
			.then((data) => setModel(data.model || id))
			.catch(() => {});
		setShowPicker(false);
	}, []);

	useEffect(() => {
		if (!showPicker) return;
		const handler = (e: MouseEvent) => {
			if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) {
				setShowPicker(false);
			}
		};
		document.addEventListener("mousedown", handler);
		return () => document.removeEventListener("mousedown", handler);
	}, [showPicker]);

	const statusText = () => {
		if (!connected) return "Disconnected";
		switch (phase) {
			case "thinking":
				return "Thinking...";
			case "streaming":
				return "Streaming...";
			case "tool_running":
				return "Running tool...";
			default:
				return "Ready";
		}
	};

	const indicatorColor = () => {
		if (!connected) return "bg-fg-dim";
		switch (phase) {
			case "thinking":
				return "bg-warning animate-[pulse_1s_infinite]";
			case "streaming":
				return "bg-accent animate-[pulse_0.5s_infinite]";
			case "tool_running":
				return "bg-orange animate-[pulse_0.8s_infinite]";
			default:
				return "bg-success";
		}
	};

	return (
		<div className="relative flex justify-between items-center h-7 px-3 border-t border-border-subtle bg-bg text-[11px] text-fg-dim shrink-0">
			<div className="flex items-center gap-2">
				<div className={`w-1.5 h-1.5 rounded-full ${indicatorColor()}`} />
				<span>{statusText()}</span>
			</div>
			<div className="flex items-center gap-3">
				{model && (
					<button
						type="button"
						className="flex items-center gap-1 hover:text-fg-muted cursor-pointer transition-colors"
						onClick={handlePickerToggle}
					>
						<span>{model}</span>
						<ChevronDown size={10} />
					</button>
				)}
				{(inputTokens > 0 || outputTokens > 0) && (
					<span className="tabular-nums">
						{"\u2191"}
						{formatTokens(inputTokens)} {"\u2193"}
						{formatTokens(outputTokens)}
					</span>
				)}
			</div>

			{/* Model picker */}
			{showPicker && (
				<div
					ref={pickerRef}
					className="absolute bottom-7 right-3 bg-bg-elevated border border-border-subtle rounded py-0.5 max-h-[200px] overflow-y-auto z-50"
				>
					{models.length === 0 ? (
						<div className="px-2 py-1 text-[11px] text-fg-dim">Loading...</div>
					) : (
						models.map((id) => (
							<button
								type="button"
								key={id}
								className={`block w-full text-left px-2.5 py-1 text-[11px] font-mono cursor-pointer whitespace-nowrap transition-colors ${
									id === model
										? "text-fg bg-bg-active"
										: "text-fg-muted hover:text-fg hover:bg-bg-hover"
								}`}
								onClick={() => selectModel(id)}
							>
								{id}
							</button>
						))
					)}
				</div>
			)}
		</div>
	);
}

function formatTokens(n: number): string {
	if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
	if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`;
	return String(n);
}
