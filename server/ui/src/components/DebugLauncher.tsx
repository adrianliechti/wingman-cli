import { Bug, Loader2, Play, Sparkles, Square, UndoDot } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import {
	controlDebug,
	discoverDebug,
	type DebugAction,
	type DebugDiscovery,
	type DebugLaunchPlan,
	type DebugSession,
	type DebugTarget,
	generateDebugPlan,
	startDebugPlan,
} from "../api/debug";
import { Dialog, dialogButtonClass } from "./ui/Feedback";

export interface DebugLauncherSeed {
	action?: DebugAction;
	target?: DebugTarget;
	currentPath?: string;
}

interface Props {
	open: boolean;
	seed?: DebugLauncherSeed;
	onClose: () => void;
	onStarted?: (session: DebugSession) => void;
}

type Phase = "loading" | "choose" | "planning" | "review" | "starting";

export function DebugLauncher({ open, seed, onClose, onStarted }: Props) {
	const requestRef = useRef<AbortController | null>(null);
	const [phase, setPhase] = useState<Phase>("loading");
	const [discovery, setDiscovery] = useState<DebugDiscovery | null>(null);
	const [action, setAction] = useState<DebugAction>("debug");
	const [adapter, setAdapter] = useState("");
	const [targetID, setTargetID] = useState("");
	const [intent, setIntent] = useState("");
	const [plan, setPlan] = useState<DebugLaunchPlan | null>(null);
	const [configurationText, setConfigurationText] = useState("{}");
	const [error, setError] = useState("");

	const requestPlan = useCallback(
		async (
			values: {
				action: DebugAction;
				adapter?: string;
				targetID?: string;
				intent?: string;
			},
			signal?: AbortSignal,
		) => {
			setPhase("planning");
			setError("");
			try {
				const generated = await generateDebugPlan(
					{
						action: values.action,
						adapter: values.adapter || undefined,
						target_id: values.targetID || undefined,
						intent: values.intent || undefined,
						current_path: seed?.currentPath,
					},
					signal,
				);
				setPlan(generated);
				setConfigurationText(JSON.stringify(generated.configuration, null, 2));
				setPhase("review");
			} catch (cause) {
				if (signal?.aborted) return;
				setError(errorMessage(cause));
				setPhase("choose");
			}
		},
		[seed?.currentPath],
	);

	useEffect(() => {
		if (!open) return;
		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		const initialAction = seed?.action ?? "debug";
		setPhase("loading");
		setDiscovery(null);
		setAction(initialAction);
		setAdapter("");
		setTargetID(seed?.target?.id ?? "");
		setIntent("");
		setPlan(null);
		setConfigurationText("{}");
		setError("");

		void discoverDebug(controller.signal)
			.then((value) => {
				if (controller.signal.aborted) return;
				setDiscovery(value);
				const selectedAdapter = value.adapters[0]?.name ?? "";
				setAdapter(selectedAdapter);
				if (
					seed?.target &&
					selectedAdapter &&
					(!value.session || value.session.state === "terminated")
				) {
					void requestPlan(
						{
							action: initialAction,
							adapter: selectedAdapter,
							targetID: seed.target.id,
						},
						controller.signal,
					);
				} else {
					setPhase("choose");
				}
			})
			.catch((cause) => {
				if (controller.signal.aborted) return;
				setError(errorMessage(cause));
				setPhase("choose");
			});
		return () => {
			controller.abort();
			requestRef.current?.abort();
		};
	}, [open, requestPlan, seed]);

	const generate = useCallback(() => {
		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		void requestPlan(
			{ action, adapter, targetID, intent: intent.trim() },
			controller.signal,
		);
	}, [action, adapter, intent, requestPlan, targetID]);

	const start = useCallback(async () => {
		if (!plan) return;
		let configuration: unknown;
		try {
			configuration = JSON.parse(configurationText);
		} catch {
			setError("Configuration must be valid JSON.");
			return;
		}
		if (!isObject(configuration)) {
			setError("Configuration must be a JSON object.");
			return;
		}
		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		setPhase("starting");
		setError("");
		try {
			const session = await startDebugPlan(
				{ ...plan, configuration },
				controller.signal,
			);
			onStarted?.(session);
			onClose();
		} catch (cause) {
			if (controller.signal.aborted) return;
			setError(errorMessage(cause));
			setPhase("review");
		}
	}, [configurationText, onClose, onStarted, plan]);

	const stopCurrent = useCallback(async () => {
		const current = discovery?.session;
		if (!current) return;
		setError("");
		try {
			await controlDebug("stop", current.session_id);
			setDiscovery((value) =>
				value ? { ...value, session: undefined } : value,
			);
		} catch (cause) {
			setError(errorMessage(cause));
		}
	}, [discovery?.session]);

	const activeSession =
		discovery?.session && discovery.session.state !== "terminated"
			? discovery.session
			: undefined;
	const selectedTarget = discovery?.targets.find(
		(target) => target.id === targetID,
	);
	const plannedAdapter = discovery?.adapters.find(
		(item) => item.name === plan?.adapter,
	);

	return (
		<Dialog
			open={open}
			title={plan?.title || "Run and debug"}
			description={
				phase === "review" && plan
					? plan.summary
					: "Describe what should run. AI will inspect the discovered targets and produce configuration for the installed debug adapter."
			}
			onClose={onClose}
			initialFocus="first"
		>
			<div className="w-full space-y-3">
				{phase === "loading" && <Busy label="Discovering debug adapters…" />}
				{activeSession && phase !== "loading" && (
					<div className="rounded-md border border-border bg-bg-surface p-3">
						<div className="flex items-center gap-2 text-[12px] text-fg">
							<Bug size={13} className="text-warning" />
							<span className="font-medium">Debug session active</span>
							<span className="ml-auto text-fg-dim">{activeSession.state}</span>
						</div>
						<div className="mt-1 text-[11px] text-fg-muted">
							{activeSession.adapter} ·{" "}
							{activeSession.mode || activeSession.request}
						</div>
						<div className="mt-3 flex justify-end">
							<button
								type="button"
								className={dialogButtonClass}
								onClick={() => void stopCurrent()}
							>
								<span className="flex items-center gap-1.5">
									<Square size={11} /> Stop current session
								</span>
							</button>
						</div>
					</div>
				)}

				{!activeSession && phase === "choose" && (
					<>
						{discovery && discovery.adapters.length === 0 ? (
							<div className="rounded-md border border-warning/30 bg-warning/5 p-3 text-[12px] text-fg-muted">
								No compatible debug adapter was found. The Go experiment looks
								for
								<code className="font-mono text-fg">dlv</code> in a Go module or
								workspace.
							</div>
						) : (
							<>
								<div className="grid grid-cols-2 gap-2">
									<label className="space-y-1 text-[11px] text-fg-muted">
										<span>Action</span>
										<select
											value={action}
											onChange={(event) =>
												setAction(event.target.value as DebugAction)
											}
											className={fieldClass}
										>
											<option value="debug">Debug</option>
											<option value="run">Run without stopping</option>
										</select>
									</label>
									<label className="space-y-1 text-[11px] text-fg-muted">
										<span>Adapter</span>
										<select
											value={adapter}
											onChange={(event) => setAdapter(event.target.value)}
											className={fieldClass}
										>
											{discovery?.adapters.map((item) => (
												<option key={item.name} value={item.name}>
													{item.language} · {item.name}
												</option>
											))}
										</select>
									</label>
								</div>
								<label className="block space-y-1 text-[11px] text-fg-muted">
									<span>Target</span>
									<select
										value={targetID}
										onChange={(event) => setTargetID(event.target.value)}
										className={fieldClass}
									>
										<option value="">Let AI choose</option>
										{discovery?.targets.map((target) => (
											<option key={target.id} value={target.id}>
												{target.name} · {target.path}:{target.line}
											</option>
										))}
									</select>
								</label>
								<label className="block space-y-1 text-[11px] text-fg-muted">
									<span>What should this session exercise?</span>
									<textarea
										value={intent}
										onChange={(event) => setIntent(event.target.value)}
										placeholder={
											selectedTarget
												? `Run ${selectedTarget.name} and stop where it begins`
												: "e.g. start the API server, or run the failing parser test"
										}
										rows={3}
										className={`${fieldClass} resize-none py-2`}
									/>
								</label>
							</>
						)}
					</>
				)}

				{phase === "planning" && <Busy label="AI is preparing the launch…" />}

				{!activeSession &&
					plan &&
					(phase === "review" || phase === "starting") && (
						<>
							<div className="grid grid-cols-4 gap-2 rounded-md border border-border bg-bg-surface p-2.5 text-[11px]">
								<PlanFact label="Adapter" value={plan.adapter} />
								<PlanFact label="Project" value={plan.project_dir} />
								<PlanFact label="Request" value={plan.request} />
								<PlanFact
									label="Console"
									value={
										plan.console === "integratedTerminal"
											? "Terminal"
											: "Debug output"
									}
								/>
							</div>
							<label className="block space-y-1 text-[11px] text-fg-muted">
								<span>Program input and output</span>
								<select
									value={plan.console}
									onChange={(event) =>
										setPlan({
											...plan,
											console: event.target.value as DebugLaunchPlan["console"],
										})
									}
									className={fieldClass}
								>
									<option value="internalConsole">Debug output</option>
									{plannedAdapter?.integrated_terminal && (
										<option value="integratedTerminal">
											Integrated terminal (interactive/TUI)
										</option>
									)}
								</select>
							</label>
							<label className="block space-y-1 text-[11px] text-fg-muted">
								<span>Adapter configuration</span>
								<textarea
									value={configurationText}
									onChange={(event) => setConfigurationText(event.target.value)}
									rows={7}
									spellCheck={false}
									className={`${fieldClass} resize-y py-2 font-mono`}
								/>
							</label>
							{(plan.breakpoints.length > 0 ||
								plan.function_breakpoints.length > 0) && (
								<div className="rounded-md border border-border-subtle px-2.5 py-2 text-[11px] text-fg-muted">
									{plan.breakpoints.map((breakpoint) => (
										<div key={`${breakpoint.file_path}:${breakpoint.line}`}>
											Stop at {breakpoint.file_path}:{breakpoint.line}
										</div>
									))}
									{plan.function_breakpoints.map((name) => (
										<div key={name}>Stop in {name}</div>
									))}
								</div>
							)}
							<div className="text-[10px] leading-relaxed text-fg-dim">
								AI generated this adapter-owned configuration. Review paths and
								arguments before starting; application code will execute.
							</div>
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
					{!activeSession &&
						phase === "choose" &&
						(discovery?.adapters.length ?? 0) > 0 && (
							<button
								type="button"
								className={`${dialogButtonClass} bg-fg text-bg hover:bg-fg-muted`}
								onClick={generate}
							>
								<span className="flex items-center gap-1.5">
									<Sparkles size={11} /> Generate launch
								</span>
							</button>
						)}
					{!activeSession && phase === "review" && plan && (
						<>
							<button
								type="button"
								className={dialogButtonClass}
								onClick={() => {
									setPlan(null);
									setPhase("choose");
								}}
							>
								<span className="flex items-center gap-1.5">
									<UndoDot size={11} /> Back
								</span>
							</button>
							<button
								type="button"
								className={`${dialogButtonClass} bg-fg text-bg hover:bg-fg-muted`}
								onClick={() => void start()}
							>
								<span className="flex items-center gap-1.5">
									{plan.action === "run" ? (
										<Play size={11} />
									) : (
										<Bug size={11} />
									)}
									{plan.action === "run" ? "Run" : "Start debugging"}
								</span>
							</button>
						</>
					)}
					{phase === "starting" && (
						<button type="button" className={dialogButtonClass} disabled>
							<span className="flex items-center gap-1.5">
								<Loader2 size={11} className="animate-spin" /> Starting…
							</span>
						</button>
					)}
				</div>
			</div>
		</Dialog>
	);
}

function Busy({ label }: { label: string }) {
	return (
		<div className="flex items-center justify-center gap-2 py-8 text-[12px] text-fg-muted">
			<Loader2 size={14} className="animate-spin" /> {label}
		</div>
	);
}

function PlanFact({ label, value }: { label: string; value: string }) {
	return (
		<div className="min-w-0">
			<div className="text-fg-dim">{label}</div>
			<div className="truncate text-fg" title={value}>
				{value}
			</div>
		</div>
	);
}

function isObject(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function errorMessage(value: unknown) {
	return value instanceof Error ? value.message : String(value);
}

const fieldClass =
	"w-full rounded-md border border-border bg-bg-input px-2.5 text-[12px] text-fg outline-none focus:border-border-strong";
