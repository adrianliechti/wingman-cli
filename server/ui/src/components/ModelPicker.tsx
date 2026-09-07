import { useMutation } from "@tanstack/react-query";
import { useCallback, useMemo, useRef, useState } from "react";
import type { SettingsPatch } from "../state/workspaceContext.ts";
import type { SessionSettings } from "../state/sessionStore.ts";
import { ModelProviderIcon } from "./ModelProviderIcon";
import { useToast } from "./ui/Feedback";
import { FloatingSurface } from "./ui/Floating";

interface Props {
	settings: SessionSettings;
	setSettings: (patch: SettingsPatch) => Promise<void>;
}

export function ModelPicker({ settings, setSettings }: Props) {
	const toast = useToast();
	const models = settings.models;
	const model = settings.model;
	const effort = settings.effort || "auto";
	const effortOptions = settings.efforts;
	const [open, setOpen] = useState(false);
	const [dragPct, setDragPct] = useState<number | null>(null);
	const [dragging, setDragging] = useState(false);
	const [button, setButton] = useState<HTMLButtonElement | null>(null);
	const trackRef = useRef<HTMLDivElement>(null);

	const modelMutation = useMutation({
		mutationFn: (id: string) => setSettings({ model: id }),
		onError: (error) =>
			toast({
				title: "Could not change model",
				description: String(error),
				tone: "error",
			}),
	});
	const effortMutation = useMutation({
		mutationFn: (value: string) => setSettings({ effort: value }),
		onError: (error) =>
			toast({
				title: "Could not change effort",
				description: String(error),
				tone: "error",
			}),
	});

	const toggle = useCallback(() => {
		setOpen((v) => !v);
	}, []);

	const selectModel = useCallback(
		(id: string) => {
			modelMutation.mutate(id);
		},
		[modelMutation],
	);

	const selectEffort = useCallback(
		(value: string) => {
			effortMutation.mutate(value);
		},
		[effortMutation],
	);

	const currentName = useMemo(() => {
		const match = models.find((m) => m.id === model);
		return match?.name || model;
	}, [models, model]);
	const currentNamespace = useMemo(
		() => models.find((m) => m.id === model)?.namespace,
		[models, model],
	);

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
		<div data-composer-model className="relative min-w-0 max-w-[260px]">
			<button
				ref={setButton}
				type="button"
				onClick={toggle}
				className="flex items-center gap-1 px-2 h-7 rounded text-[11.5px] text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors max-w-full"
				title={`${model} · ${effort}`}
				aria-haspopup="dialog"
				aria-expanded={open}
			>
				<ModelProviderIcon
					namespace={currentNamespace}
					size={12}
					className="shrink-0"
				/>
				<span className="truncate">{currentName}</span>
				{effort !== "auto" && effort !== "default" && (
					<>
						<span className="text-fg-dim">·</span>
						<span className="capitalize text-fg-dim">{effort}</span>
					</>
				)}
			</button>
			<FloatingSurface
				open={open}
				onOpenChange={setOpen}
				reference={button}
				placement="top-start"
				role="dialog"
				label="Model and reasoning effort"
				className="z-[100] min-w-[240px] max-w-[360px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl"
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
								className={`flex items-center gap-2 w-full text-left px-3 py-1.5 text-[12px] cursor-pointer whitespace-nowrap transition-colors ${
									m.id === model
										? "text-fg bg-bg-active"
										: "text-fg-muted hover:text-fg hover:bg-bg-hover"
								}`}
								onClick={() => selectModel(m.id)}
							>
								<ModelProviderIcon
									namespace={m.namespace}
									size={13}
									className="shrink-0"
								/>
								<span>{m.name}</span>
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
			</FloatingSurface>
		</div>
	);
}
