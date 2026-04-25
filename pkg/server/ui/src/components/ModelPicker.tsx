import { Brain } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

interface ModelInfo {
	id: string;
	name: string;
}

export function ModelPicker() {
	const [model, setModel] = useState("");
	const [models, setModels] = useState<ModelInfo[]>([]);
	const [open, setOpen] = useState(false);
	const popRef = useRef<HTMLDivElement>(null);
	const btnRef = useRef<HTMLButtonElement>(null);

	const loadModels = useCallback(() => {
		fetch("/api/models")
			.then((r) => r.json())
			.then((data: ModelInfo[]) => setModels(data))
			.catch(() => {});
	}, []);

	// Fetch the curated model list on mount so the button can render the
	// friendly name immediately (otherwise we'd briefly show the raw ID until
	// the dropdown is first opened).
	useEffect(() => {
		fetch("/api/model")
			.then((r) => r.json())
			.then((data) => setModel(data.model || ""))
			.catch(() => {});
		loadModels();
	}, [loadModels]);

	const toggle = useCallback(() => {
		setOpen((v) => !v);
	}, []);

	const select = useCallback((id: string) => {
		fetch("/api/model", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ model: id }),
		})
			.then((r) => r.json())
			.then((data) => setModel(data.model || id))
			.catch(() => {});
		setOpen(false);
	}, []);

	useEffect(() => {
		if (!open) return;
		const handler = (e: MouseEvent) => {
			const target = e.target as Node;
			if (
				popRef.current &&
				!popRef.current.contains(target) &&
				btnRef.current &&
				!btnRef.current.contains(target)
			) {
				setOpen(false);
			}
		};
		document.addEventListener("mousedown", handler);
		return () => document.removeEventListener("mousedown", handler);
	}, [open]);

	const currentName = useMemo(() => {
		const match = models.find((m) => m.id === model);
		return match?.name || model;
	}, [models, model]);

	if (!model) return null;

	return (
		<div className="relative">
			<button
				ref={btnRef}
				type="button"
				onClick={toggle}
				className="flex items-center gap-1 px-2 h-7 rounded text-[11.5px] text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors max-w-[220px]"
				title={model}
			>
				<Brain size={12} className="shrink-0" />
				<span className="truncate">{currentName}</span>
			</button>
			{open && (
				<div
					ref={popRef}
					className="absolute bottom-full mb-1 left-0 min-w-[220px] max-w-[360px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl py-1 max-h-[260px] overflow-y-auto z-50"
				>
					{models.length === 0 ? (
						<div className="px-3 py-2 text-[12px] text-fg-dim">Loading…</div>
					) : (
						models.map((m) => (
							<button
								type="button"
								key={m.id}
								className={`block w-full text-left px-3 py-1.5 text-[12px] cursor-pointer whitespace-nowrap transition-colors ${
									m.id === model
										? "text-fg bg-bg-active"
										: "text-fg-muted hover:text-fg hover:bg-bg-hover"
								}`}
								onClick={() => select(m.id)}
							>
								{m.name}
							</button>
						))
					)}
				</div>
			)}
		</div>
	);
}
