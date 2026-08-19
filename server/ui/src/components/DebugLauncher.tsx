import { Bug, Check, Loader2, Monitor, MonitorPlay, Play } from "lucide-react";
import {
	type ReactNode,
	useCallback,
	useEffect,
	useRef,
	useState,
} from "react";
import {
	type DebugAction,
	type DebugLaunchPlan,
	type DebugSession,
	type DebugTarget,
	generateDebugPlan,
	startDebugPlan,
} from "../api/debug";
import {
	Dialog,
	dialogButtonClass,
	dialogPrimaryButtonClass,
} from "./ui/Feedback";

export interface DebugLauncherSeed {
	action: DebugAction;
	target: DebugTarget;
	currentPath: string;
}

interface Props {
	open: boolean;
	seed?: DebugLauncherSeed;
	onClose: () => void;
	onStarted?: (session: DebugSession) => void;
}

type Phase = "planning" | "review" | "starting" | "error";

export function DebugLauncher({ open, seed, onClose, onStarted }: Props) {
	const requestRef = useRef<AbortController | null>(null);
	const [phase, setPhase] = useState<Phase>("planning");
	const [plan, setPlan] = useState<DebugLaunchPlan | null>(null);
	const [pauseAtEntry, setPauseAtEntry] = useState(false);
	const [configurationText, setConfigurationText] = useState("{}");
	const [error, setError] = useState("");

	useEffect(() => {
		if (!open) return;
		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		setPhase("planning");
		setPlan(null);
		setPauseAtEntry(false);
		setConfigurationText("{}");
		setError("");

		if (!seed) {
			setError("Choose Run or Debug beside a runnable entry point.");
			setPhase("error");
			return () => controller.abort();
		}

		void generateDebugPlan(
			{
				action: seed.action,
				target_id: seed.target.id,
				current_path: seed.currentPath,
			},
			controller.signal,
		)
			.then((generated) => {
				if (controller.signal.aborted) return;
				setPlan(generated);
				setPauseAtEntry(
					generated.breakpoints.length > 0 ||
						generated.function_breakpoints.length > 0,
				);
				setConfigurationText(JSON.stringify(generated.configuration, null, 2));
				setPhase("review");
			})
			.catch((cause) => {
				if (controller.signal.aborted) return;
				setError(errorMessage(cause));
				setPhase("error");
			});
		return () => controller.abort();
	}, [open, seed]);

	const start = useCallback(async () => {
		if (!plan) return;
		let configuration: unknown;
		try {
			configuration = JSON.parse(configurationText);
		} catch {
			setError("Adapter options must be valid JSON.");
			return;
		}
		if (!isObject(configuration)) {
			setError("Adapter options must be a JSON object.");
			return;
		}

		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		setPhase("starting");
		setError("");
		try {
			const session = await startDebugPlan(
				{
					...plan,
					configuration,
					breakpoints: pauseAtEntry ? plan.breakpoints : [],
					function_breakpoints: pauseAtEntry ? plan.function_breakpoints : [],
				},
				controller.signal,
			);
			onStarted?.(session);
			onClose();
		} catch (cause) {
			if (controller.signal.aborted) return;
			setError(errorMessage(cause));
			setPhase("review");
		}
	}, [configurationText, onClose, onStarted, pauseAtEntry, plan]);

	return (
		<Dialog
			open={open}
			title={
				plan?.title ?? (seed?.action === "run" ? "Run target" : "Debug target")
			}
			onClose={onClose}
			initialFocus="first"
		>
			<div className="w-full space-y-3">
				{phase === "planning" && <Busy label="Preparing launch…" />}

				{plan && (phase === "review" || phase === "starting") && (
					<>
						<div className="space-y-1.5">
							<div className="text-[10px] font-medium uppercase tracking-wide text-fg-dim">
								Program I/O
							</div>
							<div
								className={`grid gap-1 ${plan.terminal_available ? "grid-cols-2" : "grid-cols-1"}`}
							>
								<ActionChoice
									selected={plan.io === "output"}
									icon={<MonitorPlay size={12} />}
									label="Debug output"
									detail="Capture in the Debug tab"
									onClick={() => setPlan({ ...plan, io: "output" })}
								/>
								{plan.terminal_available && (
									<ActionChoice
										selected={plan.io === "terminal"}
										icon={<Monitor size={12} />}
										label="Terminal"
										detail="Interactive input and TUIs"
										onClick={() => setPlan({ ...plan, io: "terminal" })}
									/>
								)}
							</div>
						</div>
						{(plan.breakpoints.length > 0 ||
							plan.function_breakpoints.length > 0) && (
							<ToggleOption
								selected={pauseAtEntry}
								label="Pause at entry"
								detail={entryBreakpointLabel(plan)}
								onClick={() => setPauseAtEntry((current) => !current)}
							/>
						)}
						<details className="rounded-md border border-border-subtle bg-bg-surface/30 text-[11px] text-fg-muted">
							<summary className="cursor-pointer px-2.5 py-2 marker:text-fg-dim hover:text-fg">
								Adapter options (JSON)
							</summary>
							<div className="space-y-2 border-t border-border-subtle p-2">
								<div className="text-[10px] leading-relaxed text-fg-dim">
									Adapter-defined DAP launch arguments.
								</div>
								<textarea
									aria-label="Adapter launch options"
									value={configurationText}
									onChange={(event) => setConfigurationText(event.target.value)}
									rows={6}
									spellCheck={false}
									className={`${fieldClass} resize-y py-2 font-mono`}
								/>
							</div>
						</details>
					</>
				)}

				{error && (
					<div className="whitespace-pre-wrap break-words rounded-md border border-danger/30 bg-danger/5 px-2.5 py-2 text-[11px] text-danger">
						{error}
					</div>
				)}

				<div className="flex justify-end gap-2 pt-1">
					<button type="button" className={dialogButtonClass} onClick={onClose}>
						Cancel
					</button>
					{phase === "review" && plan && (
						<button
							type="button"
							className={dialogPrimaryButtonClass}
							onClick={() => void start()}
						>
							{plan.action === "run" ? <Play size={12} /> : <Bug size={12} />}
							{plan.action === "run" ? "Run" : "Start debugging"}
						</button>
					)}
					{phase === "starting" && (
						<button type="button" className={dialogPrimaryButtonClass} disabled>
							<Loader2 size={12} className="animate-spin" /> Starting…
						</button>
					)}
				</div>
			</div>
		</Dialog>
	);
}

function ActionChoice({
	selected,
	icon,
	label,
	detail,
	onClick,
}: {
	selected: boolean;
	icon: ReactNode;
	label: string;
	detail: string;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			aria-pressed={selected}
			onClick={onClick}
			className={`flex min-w-0 items-start gap-2 rounded-md border px-2.5 py-2 text-left transition-colors ${
				selected
					? "border-accent/50 bg-accent/10 text-fg"
					: "border-transparent text-fg-muted hover:border-border-subtle hover:bg-bg-hover hover:text-fg"
			}`}
		>
			<span
				className={`mt-0.5 shrink-0 ${selected ? "text-accent" : "text-fg-dim"}`}
			>
				{icon}
			</span>
			<span className="min-w-0">
				<span className="block text-[11.5px] font-medium">{label}</span>
				<span className="mt-0.5 block truncate text-[9.5px] text-fg-dim">
					{detail}
				</span>
			</span>
		</button>
	);
}

function ToggleOption({
	selected,
	label,
	detail,
	onClick,
}: {
	selected: boolean;
	label: string;
	detail: string;
	onClick: () => void;
}) {
	return (
		<button
			type="button"
			aria-pressed={selected}
			onClick={onClick}
			className="flex w-full min-w-0 items-center gap-2.5 rounded-md border border-border-subtle bg-bg-surface/30 px-2.5 py-2 text-left text-fg-muted transition-colors hover:border-border-strong hover:bg-bg-hover hover:text-fg"
		>
			<span
				className={`flex h-4 w-4 shrink-0 items-center justify-center rounded border ${
					selected
						? "border-accent bg-accent text-bg"
						: "border-border-strong bg-bg-input"
				}`}
			>
				{selected && <Check size={10} strokeWidth={3} />}
			</span>
			<span className="min-w-0 flex-1">
				<span className="block text-[11.5px] font-medium">{label}</span>
				<span className="mt-0.5 block truncate text-[9.5px] text-fg-dim">
					{detail}
				</span>
			</span>
		</button>
	);
}

function Busy({ label }: { label: string }) {
	return (
		<div className="flex items-center gap-2 rounded-md border border-border-subtle bg-bg-surface/30 px-3 py-3 text-[11px] text-fg-muted">
			<Loader2 size={12} className="animate-spin" /> {label}
		</div>
	);
}

function entryBreakpointLabel(plan: DebugLaunchPlan) {
	if (plan.breakpoints.length === 1) {
		const breakpoint = plan.breakpoints[0];
		return `${breakpoint.file_path}:${breakpoint.line}`;
	}
	const count = plan.breakpoints.length + plan.function_breakpoints.length;
	return `${count} generated entry breakpoints`;
}

function isObject(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

function errorMessage(value: unknown) {
	return value instanceof Error ? value.message : String(value);
}

const fieldClass =
	"w-full rounded-md border border-border-subtle bg-bg-input px-2.5 text-[11px] text-fg outline-none focus:border-accent/50 focus:ring-1 focus:ring-accent/20";
