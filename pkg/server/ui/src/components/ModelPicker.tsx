import { ChevronDown } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

export function ModelPicker() {
	const [model, setModel] = useState("");
	const [models, setModels] = useState<string[]>([]);
	const [open, setOpen] = useState(false);
	const popRef = useRef<HTMLDivElement>(null);
	const btnRef = useRef<HTMLButtonElement>(null);

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

	const toggle = useCallback(() => {
		if (!open) loadModels();
		setOpen((v) => !v);
	}, [open, loadModels]);

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
				<span className="truncate font-mono">{model}</span>
				<ChevronDown size={11} className="shrink-0" />
			</button>
			{open && (
				<div
					ref={popRef}
					className="absolute bottom-full mb-1 left-0 min-w-[220px] max-w-[360px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl py-1 max-h-[260px] overflow-y-auto z-50"
				>
					{models.length === 0 ? (
						<div className="px-3 py-2 text-[12px] text-fg-dim">Loading…</div>
					) : (
						models.map((id) => (
							<button
								type="button"
								key={id}
								className={`block w-full text-left px-3 py-1.5 text-[12px] font-mono cursor-pointer whitespace-nowrap transition-colors ${
									id === model
										? "text-fg bg-bg-active"
										: "text-fg-muted hover:text-fg hover:bg-bg-hover"
								}`}
								onClick={() => select(id)}
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
