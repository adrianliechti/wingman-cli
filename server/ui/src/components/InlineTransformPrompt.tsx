import { Loader2, MessageSquareCode, Sparkles } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { FloatingSurface } from "./ui/Floating";

interface Point {
	x: number;
	y: number;
}

interface Props {
	reference: Point;
	selectionLabel: string;
	onTransform: (instruction: string, signal: AbortSignal) => Promise<void>;
	onAskWingman: () => void;
	onClose: () => void;
}

const presets = [
	{
		label: "Fix",
		instruction:
			"Fix correctness issues in this selection while preserving its intended behavior.",
	},
	{
		label: "Refactor",
		instruction:
			"Refactor this selection for clarity and maintainability without changing its behavior.",
	},
	{
		label: "Document",
		instruction:
			"Add or improve concise, useful documentation for this selection.",
	},
];

export function InlineTransformPrompt({
	reference,
	selectionLabel,
	onTransform,
	onAskWingman,
	onClose,
}: Props) {
	const [instruction, setInstruction] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const controllerRef = useRef<AbortController | null>(null);

	useEffect(
		() => () => {
			controllerRef.current?.abort();
		},
		[],
	);

	const submit = async (value: string) => {
		value = value.trim();
		if (!value || busy) return;
		const controller = new AbortController();
		controllerRef.current?.abort();
		controllerRef.current = controller;
		setBusy(true);
		setError("");
		try {
			await onTransform(value, controller.signal);
			onClose();
		} catch (reason) {
			if (!controller.signal.aborted) {
				setError(reason instanceof Error ? reason.message : String(reason));
			}
		} finally {
			if (controllerRef.current === controller) controllerRef.current = null;
			setBusy(false);
		}
	};

	return (
		<FloatingSurface
			open
			onOpenChange={(open) => !open && onClose()}
			reference={reference}
			placement="bottom-start"
			label="Transform selection"
			focusOnOpen
			className="z-[150] w-[390px] rounded-lg border border-border-subtle bg-bg-elevated p-2 shadow-2xl"
		>
			<form
				onSubmit={(event) => {
					event.preventDefault();
					void submit(instruction);
				}}
			>
				<div className="flex items-center gap-2 px-1 pb-1.5">
					<Sparkles size={13} className="shrink-0 text-accent" />
					<span className="min-w-0 flex-1 truncate text-[11px] text-fg-muted">
						{selectionLabel}
					</span>
				</div>
				<div className="flex items-center rounded-md border border-border bg-bg-input focus-within:border-border-strong">
					<input
						autoFocus
						value={instruction}
						onChange={(event) => setInstruction(event.target.value)}
						placeholder="Describe the transformation…"
						aria-label="Transformation instruction"
						disabled={busy}
						className="h-8 min-w-0 flex-1 bg-transparent px-2 text-[12px] text-fg outline-none placeholder:text-fg-dim"
					/>
					<button
						type="submit"
						disabled={busy || !instruction.trim()}
						className="mr-0.5 flex h-7 items-center gap-1 rounded px-2 text-[10.5px] text-fg-muted hover:bg-bg-hover hover:text-fg disabled:cursor-not-allowed disabled:opacity-30"
					>
						{busy ? <Loader2 size={11} className="animate-spin" /> : "Generate"}
					</button>
				</div>
				<div className="mt-1.5 flex items-center gap-1">
					{presets.map((preset) => (
						<button
							key={preset.label}
							type="button"
							disabled={busy}
							onClick={() => void submit(preset.instruction)}
							className="rounded px-2 py-1 text-[10.5px] text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-40"
						>
							{preset.label}
						</button>
					))}
					<div className="flex-1" />
					<button
						type="button"
						disabled={busy}
						onClick={() => {
							onAskWingman();
							onClose();
						}}
						className="flex items-center gap-1 rounded px-2 py-1 text-[10.5px] text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-40"
					>
						<MessageSquareCode size={11} />
						Chat about this…
					</button>
				</div>
				{error && (
					<div role="alert" className="mt-1.5 px-1 text-[10.5px] text-danger">
						{error}
					</div>
				)}
			</form>
		</FloatingSurface>
	);
}
