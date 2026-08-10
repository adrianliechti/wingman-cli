import { Brain } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ServerMessage } from "../types/protocol";
import { useToast } from "./ui/Feedback";

interface ModelInfo {
	id: string;
	name: string;
}

interface Props {
	sessionId?: string;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

export function ModelPicker({ sessionId, subscribe }: Props) {
	const toast = useToast();
	const [model, setModel] = useState("");
	const [models, setModels] = useState<ModelInfo[]>([]);
	const [effort, setEffort] = useState("auto");
	const [effortOptions, setEffortOptions] = useState<string[]>([]);
	const [open, setOpen] = useState(false);
	const [dragPct, setDragPct] = useState<number | null>(null);
	const [dragging, setDragging] = useState(false);
	const popRef = useRef<HTMLDivElement>(null);
	const btnRef = useRef<HTMLButtonElement>(null);
	const trackRef = useRef<HTMLDivElement>(null);

	const applyEffort = useCallback((v: unknown) => {
		if (typeof v === "string" && v !== "") {
			setEffort(v);
		} else {
			setEffort("auto");
		}
	}, []);

	const loadModels = useCallback(() => {
		fetch("/api/models")
			.then((r) => r.json())
			.then((data: ModelInfo[]) => setModels(data))
			.catch(() => setModels([]));
	}, []);

	const apiBase = sessionId
		? `/api/sessions/${encodeURIComponent(sessionId)}`
		: "/api";

	const loadCurrent = useCallback(() => {
		fetch(`${apiBase}/model`)
			.then((r) => r.json())
			.then((data) => setModel(data.model || ""))
			.catch(() => {});
		fetch(`${apiBase}/effort`)
			.then((r) => r.json())
			.then((data) => {
				applyEffort(data.effort);
				setEffortOptions(Array.isArray(data.options) ? data.options : []);
			})
			.catch(() => {});
	}, [applyEffort, apiBase]);

	useEffect(() => {
		loadCurrent();
		loadModels();
	}, [loadCurrent, loadModels]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "agent_changed" || msg.type === "model_changed") {
				loadCurrent();
				loadModels();
			}
		});
	}, [subscribe, loadCurrent, loadModels]);

	const toggle = useCallback(() => {
		setOpen((v) => !v);
	}, []);

	const selectModel = useCallback(
		(id: string) => {
			fetch(`${apiBase}/model`, {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ model: id }),
			})
				.then(async (r) => {
					if (!r.ok) throw new Error(await r.text());
					return r.json();
				})
				.then((data) => setModel(data.model || id))
				.catch((error) =>
					toast({
						title: "Could not change model",
						description: String(error),
						tone: "error",
					}),
				);
		},
		[apiBase, toast],
	);

	const selectEffort = useCallback(
		(value: string) => {
			fetch(`${apiBase}/effort`, {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ effort: value }),
			})
				.then(async (r) => {
					if (!r.ok) throw new Error(await r.text());
					return r.json();
				})
				.then((data) => applyEffort(data.effort))
				.catch((error) =>
					toast({
						title: "Could not change effort",
						description: String(error),
						tone: "error",
					}),
				);
		},
		[applyEffort, apiBase, toast],
	);

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

	const defaultEffort = useMemo(
		() =>
			effortOptions.find((v) => v === "default" || v === "auto") ?? "default",
		[effortOptions],
	);
	const efforts = useMemo(
		() => effortOptions.filter((v) => v !== defaultEffort),
		[effortOptions, defaultEffort],
	);
	const steps = useMemo(
		() => [defaultEffort, ...efforts],
		[defaultEffort, efforts],
	);
	const stepIndex = Math.max(0, steps.indexOf(effort));
	const pct =
		dragPct ?? (steps.length > 1 ? (stepIndex / (steps.length - 1)) * 100 : 0);
	const previewIndex =
		steps.length > 1 ? Math.round((pct / 100) * (steps.length - 1)) : 0;
	const knobLabel = previewIndex === 0 ? "Auto" : steps[previewIndex];

	const commit = useCallback(
		(index: number) => {
			const value = steps[index];
			if (value !== undefined && value !== effort) selectEffort(value);
		},
		[steps, effort, selectEffort],
	);

	const handlePointerDown = useCallback(
		(e: React.PointerEvent) => {
			const track = trackRef.current;
			if (!track || steps.length < 2) return;
			const rect = track.getBoundingClientRect();
			const toPct = (clientX: number) =>
				Math.min(100, Math.max(0, ((clientX - rect.left) / rect.width) * 100));
			setDragging(true);
			setDragPct(toPct(e.clientX));
			const onMove = (ev: PointerEvent) => setDragPct(toPct(ev.clientX));
			const onUp = (ev: PointerEvent) => {
				window.removeEventListener("pointermove", onMove);
				window.removeEventListener("pointerup", onUp);
				const finalPct = toPct(ev.clientX);
				const index = Math.round((finalPct / 100) * (steps.length - 1));
				setDragging(false);
				setDragPct(null);
				commit(index);
			};
			window.addEventListener("pointermove", onMove);
			window.addEventListener("pointerup", onUp);
		},
		[steps, commit],
	);

	const handleKeyDown = useCallback(
		(e: React.KeyboardEvent) => {
			let index = stepIndex;
			if (e.key === "ArrowLeft" || e.key === "ArrowDown") {
				index = Math.max(0, stepIndex - 1);
			} else if (e.key === "ArrowRight" || e.key === "ArrowUp") {
				index = Math.min(steps.length - 1, stepIndex + 1);
			} else if (e.key === "Home") {
				index = 0;
			} else if (e.key === "End") {
				index = steps.length - 1;
			} else {
				return;
			}
			e.preventDefault();
			commit(index);
		},
		[stepIndex, steps, commit],
	);

	if (!model) return null;

	return (
		<div className="relative">
			<button
				ref={btnRef}
				type="button"
				onClick={toggle}
				className="flex items-center gap-1 px-2 h-7 rounded text-[11.5px] text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors max-w-[260px]"
				title={`${model} · ${effort}`}
				aria-haspopup="dialog"
				aria-expanded={open}
			>
				<Brain size={12} className="shrink-0" />
				<span className="truncate">{currentName}</span>
				{effort !== "auto" && effort !== "default" && (
					<>
						<span className="text-fg-dim">·</span>
						<span className="capitalize text-fg-dim">{effort}</span>
					</>
				)}
			</button>
			{open && (
				<div
					ref={popRef}
					role="dialog"
					aria-label="Model and reasoning effort"
					className="absolute bottom-full mb-1 left-0 min-w-[240px] max-w-[360px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl z-50"
				>
					<div
						className="py-1 max-h-[260px] overflow-y-auto"
						role="listbox"
						aria-label="Model"
					>
						{models.length === 0 ? (
							<div className="px-3 py-2 text-[12px] text-fg-dim">Loading…</div>
						) : (
							models.map((m) => (
								<button
									type="button"
									role="option"
									aria-selected={m.id === model}
									key={m.id}
									className={`block w-full text-left px-3 py-1.5 text-[12px] cursor-pointer whitespace-nowrap transition-colors ${
										m.id === model
											? "text-fg bg-bg-active"
											: "text-fg-muted hover:text-fg hover:bg-bg-hover"
									}`}
									onClick={() => selectModel(m.id)}
								>
									{m.name}
								</button>
							))
						)}
					</div>
					{efforts.length > 0 && (
						<div className="border-t border-border px-3 h-9 flex items-center">
							<div
								ref={trackRef}
								onPointerDown={handlePointerDown}
								className="relative flex-1 h-6 flex items-center cursor-pointer touch-none"
							>
								<div className="absolute inset-x-0 h-[2px] rounded-full bg-bg-active" />
								<div
									className="absolute left-0 h-[2px] rounded-full bg-fg-muted/70"
									style={{ width: `${pct}%` }}
								/>
								<div className="absolute inset-0 flex items-center justify-between pointer-events-none">
									{steps.map((v, i) => (
										<span
											key={v}
											className={`w-[3px] h-[3px] rounded-full transition-colors ${
												i <= previewIndex ? "bg-fg-muted/70" : "bg-fg-dim/40"
											}`}
										/>
									))}
								</div>
								<div
									role="slider"
									tabIndex={0}
									aria-label="Reasoning effort"
									aria-valuemin={0}
									aria-valuemax={steps.length - 1}
									aria-valuenow={previewIndex}
									aria-valuetext={knobLabel}
									onKeyDown={handleKeyDown}
									className={`absolute top-1/2 flex items-center justify-center h-5 px-1.5 rounded-[5px] bg-fg text-bg text-[10px] font-semibold capitalize leading-none whitespace-nowrap shadow-sm cursor-grab active:cursor-grabbing ${
										dragging ? "" : "transition-[left] duration-150 ease-out"
									}`}
									style={{
										left: `${pct}%`,
										transform: `translate(-${pct}%, -50%)`,
									}}
								>
									{knobLabel}
								</div>
							</div>
						</div>
					)}
				</div>
			)}
		</div>
	);
}
