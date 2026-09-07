import {
	Bug,
	Check,
	Download,
	Loader2,
	Monitor,
	MonitorPlay,
	Play,
} from "lucide-react";
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
	type DebugToolProgress,
	type DebugToolStatus,
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
	onFailed?: (message: string) => void;
}

type Phase = "planning" | "install" | "review" | "starting" | "error";

export function DebugLauncher({ open, seed, ...callbacks }: Props) {
	const requestKey = open
		? seed
			? JSON.stringify([seed.action, seed.target.id, seed.currentPath])
			: "missing"
		: "closed";
	return (
		<DebugLauncherContent
			key={requestKey}
			open={open}
			seed={seed}
			{...callbacks}
		/>
	);
}

function DebugLauncherContent({
	open,
	seed,
	onClose,
	onStarted,
	onFailed,
}: Props) {
	const requestRef = useRef<AbortController | null>(null);
	const [phase, setPhase] = useState<Phase>(seed ? "planning" : "error");
	const [plan, setPlan] = useState<DebugLaunchPlan | null>(null);
	const [progress, setProgress] = useState<DebugToolProgress | null>(null);
	const [tools, setTools] = useState<DebugToolStatus[]>([]);
	const [installRequested, setInstallRequested] = useState(false);
	const [pauseAtEntry, setPauseAtEntry] = useState(false);
	const [configurationText, setConfigurationText] = useState("{}");
	const [error, setError] = useState(
		seed ? "" : "Choose Run or Debug beside a runnable entry point.",
	);
	const [attempt, setAttempt] = useState(0);
	const action = seed?.action;
	const currentPath = seed?.currentPath;
	const targetID = seed?.target.id;
	const canInstall =
		tools.some((tool) => !tool.installed) &&
		tools.every((tool) => tool.installed || tool.installable);

	useEffect(() => {
		if (!open || !action || currentPath === undefined || !targetID) return;
		const controller = new AbortController();
		requestRef.current = controller;

		void generateDebugPlan(
			{
				action,
				target_id: targetID,
				current_path: currentPath,
				install: installRequested,
			},
			controller.signal,
			(update) => {
				if (!controller.signal.aborted) setProgress(update);
			},
			(statuses) => {
				if (!controller.signal.aborted) setTools(statuses);
			},
		)
			.then((result) => {
				if (controller.signal.aborted) return;
				if (result.type === "installation_required") {
					setTools(result.tools);
					setPhase("install");
					return;
				}
				const generated = result.plan;
				setError(result.warning ?? "");
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
		return () => {
			controller.abort();
			requestRef.current?.abort();
		};
	}, [action, attempt, currentPath, installRequested, open, targetID]);

	const close = useCallback(() => {
		requestRef.current?.abort();
		onClose();
	}, [onClose]);

	const retry = useCallback(() => {
		requestRef.current?.abort();
		setPhase("planning");
		setPlan(null);
		setProgress(null);
		setPauseAtEntry(false);
		setConfigurationText("{}");
		setError("");
		setAttempt((value) => value + 1);
	}, []);

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
			if (controller.signal.aborted) return;
			onStarted?.(session);
			close();
		} catch (cause) {
			if (controller.signal.aborted) return;
			const message = errorMessage(cause);
			if (onFailed) {
				onFailed(message);
				close();
				return;
			}
			setError(message);
			setPhase("review");
		}
	}, [close, configurationText, onFailed, onStarted, pauseAtEntry, plan]);

	return (
		<Dialog
			open={open}
			title={
				plan?.title ??
				`${seed?.action === "run" ? "Run" : "Debug"} ${seed?.target.name ?? "target"}`
			}
			onClose={close}
			initialFocus="first"
		>
			<div className="w-full space-y-3">
				{tools.length > 0 && (
					<ul
						aria-label="Debugger setup"
						className="space-y-2 rounded-md border border-border-subtle bg-bg-surface/30 px-3 py-2.5 text-[11px]"
					>
						{tools.map((tool) => (
							<li
								key={tool.tool}
								className="flex items-center justify-between gap-3"
							>
								<span className="text-fg-muted">{tool.label}</span>
								<span className="flex items-center gap-1.5 text-fg-dim">
									{phase === "planning" &&
									progress?.tool === tool.tool &&
									progress.phase === "updating" ? (
										"Updating…"
									) : tool.installed ? (
										<>
											<Check size={12} className="text-success" /> Installed
										</>
									) : phase === "planning" &&
									  progress?.tool === tool.tool &&
									  progress.phase !== "checking" ? (
										"Installing…"
									) : phase === "error" && installRequested && progress ? (
										"Installation failed"
									) : tool.unavailable_reason ? (
										tool.unavailable_reason
									) : (
										"Not installed"
									)}
								</span>
							</li>
						))}
					</ul>
				)}
				{phase === "install" && (
					<p className="text-[11px] leading-relaxed text-fg-muted">
						{canInstall
							? "Install the missing debugger tools in Wingman to continue?"
							: tools.some((tool) => tool.unavailable_reason)
								? "Install the required runtime or build tool, then check again."
								: "Wingman's debugger installation is disabled. Enable managed tool installation, then check again."}
					</p>
				)}
				{phase === "planning" && (
					<Busy
						label={
							progress
								? `${progress.phase === "checking" ? "Checking" : progress.phase === "updating" ? "Updating" : "Installing"} ${progress.label}…`
								: installRequested
									? "Preparing installation…"
									: "Checking debugger…"
						}
						detail={
							progress && progress.phase !== "checking"
								? "This may take a few minutes."
								: undefined
						}
					/>
				)}

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
									autoCapitalize="none"
									autoCorrect="off"
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
					<button type="button" className={dialogButtonClass} onClick={close}>
						Cancel
					</button>
					{phase === "install" && (
						<button
							type="button"
							className={dialogPrimaryButtonClass}
							onClick={() => {
								if (canInstall) setInstallRequested(true);
								retry();
							}}
						>
							{canInstall && <Download size={12} />}
							{canInstall ? "Install debugger" : "Check again"}
						</button>
					)}
					{phase === "error" && seed && (
						<button
							type="button"
							className={dialogPrimaryButtonClass}
							onClick={retry}
						>
							Retry
						</button>
					)}
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
	onClick,
}: {
	selected: boolean;
	label: string;
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
			<span className="min-w-0 flex-1 text-[11.5px] font-medium">{label}</span>
		</button>
	);
}

function Busy({ label, detail }: { label: string; detail?: string }) {
	return (
		<div
			role="status"
			className="rounded-md border border-border-subtle bg-bg-surface/30 px-3 py-3 text-[11px] text-fg-muted"
		>
			<div className="flex items-center gap-2">
				<Loader2 size={12} className="shrink-0 animate-spin" /> {label}
			</div>
			{detail && <p className="mt-1.5 text-fg-dim">{detail}</p>}
		</div>
	);
}

function isObject(value: unknown): value is Record<string, unknown> {
	return !!value && typeof value === "object" && !Array.isArray(value);
}

function errorMessage(value: unknown) {
	const message = value instanceof Error ? value.message : String(value);
	return message
		? message[0].toUpperCase() + message.slice(1)
		: "The debugger could not start.";
}

const fieldClass =
	"w-full rounded-md border border-border-subtle bg-bg-input px-2.5 text-[11px] text-fg outline-none focus:border-accent/50 focus:ring-1 focus:ring-accent/20";
