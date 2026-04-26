import {
	ChevronDown,
	PanelLeftClose,
	PanelLeftOpen,
	PanelRightClose,
	PanelRightOpen,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

interface Props {
	inputTokens: number;
	outputTokens: number;
	leftWidth: number;
	rightWidth: number;
	sidebarCollapsed: boolean;
	rightPanelCollapsed: boolean;
	onToggleSidebar: () => void;
	onToggleRightPanel: () => void;
}

export function StatusBar({
	inputTokens,
	outputTokens,
	leftWidth,
	rightWidth,
	sidebarCollapsed,
	rightPanelCollapsed,
	onToggleSidebar,
	onToggleRightPanel,
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

	return (
		<div className="relative flex h-9 border-t border-border-subtle bg-bg text-[12px] text-fg-dim shrink-0">
			{/* Left section — mirrors sidebar width; hosts the hide-sidebar toggle when open */}
			<div
				className="shrink-0 overflow-hidden transition-[width] duration-200 ease-in-out border-r border-border-subtle"
				style={{
					width: sidebarCollapsed ? 0 : leftWidth,
					borderRightWidth: sidebarCollapsed ? 0 : 1,
				}}
			>
				<div
					className="h-full flex items-center justify-end pr-1"
					style={{ width: leftWidth }}
				>
					<button
						type="button"
						className="flex items-center justify-center w-9 h-9 text-fg-dim hover:text-fg-muted cursor-pointer transition-colors shrink-0"
						onClick={onToggleSidebar}
						title="Hide sidebar"
					>
						<PanelLeftClose size={15} />
					</button>
				</div>
			</div>

			{/* Main section — toggles appear here only when their drawer is collapsed */}
			<div className="flex-1 flex items-center min-w-0">
				{sidebarCollapsed && (
					<button
						type="button"
						className="flex items-center justify-center w-9 h-9 text-fg-dim hover:text-fg-muted cursor-pointer transition-colors shrink-0"
						onClick={onToggleSidebar}
						title="Show sidebar"
					>
						<PanelLeftOpen size={15} />
					</button>
				)}

				<div className="flex items-center px-2 min-w-0">
					{model && (
						<button
							type="button"
							className="flex items-center gap-1 hover:text-fg-muted cursor-pointer transition-colors min-w-0"
							onClick={handlePickerToggle}
						>
							<span className="truncate">{model}</span>
							<ChevronDown size={12} className="shrink-0" />
						</button>
					)}
				</div>

				<div className="flex-1" />

				<div className="flex items-center px-2">
					{(inputTokens > 0 || outputTokens > 0) && (
						<span className="tabular-nums">
							{"\u2191"}
							{formatTokens(inputTokens)} {"\u2193"}
							{formatTokens(outputTokens)}
						</span>
					)}
				</div>

				{rightPanelCollapsed && (
					<button
						type="button"
						className="flex items-center justify-center w-9 h-9 text-fg-dim hover:text-fg-muted cursor-pointer transition-colors shrink-0"
						onClick={onToggleRightPanel}
						title="Show panel"
					>
						<PanelRightOpen size={15} />
					</button>
				)}
			</div>

			{/* Right section — mirrors right panel width; hosts the hide-panel toggle when open */}
			<div
				className="shrink-0 overflow-hidden transition-[width] duration-200 ease-in-out border-l border-border-subtle"
				style={{
					width: rightPanelCollapsed ? 0 : rightWidth,
					borderLeftWidth: rightPanelCollapsed ? 0 : 1,
				}}
			>
				<div
					className="h-full flex items-center justify-start pl-1"
					style={{ width: rightWidth }}
				>
					<button
						type="button"
						className="flex items-center justify-center w-9 h-9 text-fg-dim hover:text-fg-muted cursor-pointer transition-colors shrink-0"
						onClick={onToggleRightPanel}
						title="Hide panel"
					>
						<PanelRightClose size={15} />
					</button>
				</div>
			</div>

			{/* Model picker */}
			{showPicker && (
				<div
					ref={pickerRef}
					className="absolute bottom-9 bg-bg-elevated border border-border-subtle rounded py-0.5 max-h-[200px] overflow-y-auto z-50"
					style={{ left: sidebarCollapsed ? 36 : leftWidth + 8 }}
				>
					{models.length === 0 ? (
						<div className="px-2 py-1 text-[12px] text-fg-dim">Loading...</div>
					) : (
						models.map((id) => (
							<button
								type="button"
								key={id}
								className={`block w-full text-left px-2.5 py-1 text-[12px] font-mono cursor-pointer whitespace-nowrap transition-colors ${
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
