import {
	ArrowDownToDot,
	Bug,
	ChevronDown,
	ChevronRight,
	Loader2,
	Pause,
	Play,
	RedoDot,
	RefreshCw,
	Square,
	StepBack,
	UndoDot,
} from "lucide-react";
import {
	type ReactNode,
	useCallback,
	useEffect,
	useRef,
	useState,
} from "react";
import {
	controlDebug,
	getDebugInspection,
	getDebugScopes,
	getDebugVariables,
	type DebugInspection,
	type DebugScopeInspection,
	type DebugStackFrame,
	type DebugVariable,
} from "../api/debug";

interface Props {
	onLaunch: () => void;
	onOpenFile: (path: string, line: number, column: number) => void;
	onStopped?: () => void;
}

type DebugOperation =
	| "continue"
	| "next"
	| "stepIn"
	| "stepOut"
	| "stepBack"
	| "pause"
	| "stop";

export function DebugTab({ onLaunch, onOpenFile, onStopped }: Props) {
	const requestsRef = useRef(new Set<AbortController>());
	const refreshSequenceRef = useRef(0);
	const inspectionRef = useRef<DebugInspection | null>(null);
	const selectedFrameRef = useRef<number | undefined>(undefined);
	const scopeRequestRef = useRef<AbortController | null>(null);
	const stopVersionRef = useRef("");
	const outputRef = useRef<HTMLPreElement | null>(null);
	const onOpenFileRef = useRef(onOpenFile);
	const onStoppedRef = useRef(onStopped);
	const busyCountRef = useRef(0);
	const [inspection, setInspection] = useState<DebugInspection | null>(null);
	const [selectedFrameID, setSelectedFrameID] = useState<number>();
	const [scopes, setScopes] = useState<DebugScopeInspection[]>([]);
	const [error, setError] = useState("");
	const [busy, setBusy] = useState(false);
	selectedFrameRef.current = selectedFrameID;
	onOpenFileRef.current = onOpenFile;
	onStoppedRef.current = onStopped;

	const beginBusy = useCallback(() => {
		busyCountRef.current += 1;
		setBusy(true);
		return () => {
			busyCountRef.current = Math.max(0, busyCountRef.current - 1);
			if (busyCountRef.current === 0) setBusy(false);
		};
	}, []);

	const track = useCallback(
		async <T,>(controller: AbortController, request: Promise<T>) => {
			requestsRef.current.add(controller);
			try {
				return await request;
			} finally {
				requestsRef.current.delete(controller);
			}
		},
		[],
	);

	const applyInspection = useCallback((next: DebugInspection) => {
		const current = inspectionRef.current;
		if (!next.session && current?.session?.state === "terminated") return false;
		if (
			next.session &&
			current?.session?.session_id === next.session.session_id &&
			next.session.state_version < current.session.state_version
		)
			return false;

		inspectionRef.current = next;
		setInspection(next);
		if (next.session?.state !== "stopped" || next.frames.length === 0) {
			stopVersionRef.current = "";
			setSelectedFrameID(undefined);
			setScopes([]);
			return true;
		}
		const stopVersion = `${next.session.session_id}:${next.session.state_version}`;
		if (stopVersion !== stopVersionRef.current) {
			stopVersionRef.current = stopVersion;
			setSelectedFrameID(next.frames[0].id);
			setScopes(next.scopes);
			const frame = next.frames[0];
			if (frame.source?.path) {
				onOpenFileRef.current(frame.source.path, frame.line, frame.column);
			}
			onStoppedRef.current?.();
			return true;
		}
		const selected =
			next.frames.find((frame) => frame.id === selectedFrameRef.current) ??
			next.frames[0];
		setSelectedFrameID(selected.id);
		if (selected.id === next.frames[0].id) setScopes(next.scopes);
		return true;
	}, []);

	const refresh = useCallback(
		async (showBusy = false) => {
			const sequence = ++refreshSequenceRef.current;
			const controller = new AbortController();
			const endBusy = showBusy ? beginBusy() : undefined;
			try {
				const next = await track(
					controller,
					getDebugInspection(controller.signal),
				);
				if (sequence !== refreshSequenceRef.current) return null;
				if (applyInspection(next)) setError(next.error ?? "");
				return next;
			} catch (cause) {
				if (!controller.signal.aborted) setError(errorMessage(cause));
				return null;
			} finally {
				endBusy?.();
			}
		},
		[applyInspection, beginBusy, track],
	);

	useEffect(() => {
		let disposed = false;
		let timer = 0;
		const requests = requestsRef.current;
		const poll = async () => {
			const next = await refresh();
			if (disposed) return;
			const delay = next?.session?.state === "running" ? 600 : 1_500;
			timer = window.setTimeout(() => void poll(), delay);
		};
		void poll();
		return () => {
			disposed = true;
			window.clearTimeout(timer);
			scopeRequestRef.current?.abort();
			for (const request of requests) request.abort();
			requests.clear();
		};
	}, [refresh]);

	useEffect(() => {
		const output = outputRef.current;
		if (output) output.scrollTop = output.scrollHeight;
	}, [inspection?.output]);

	const selectFrame = useCallback(
		async (frame: DebugStackFrame) => {
			const current = inspectionRef.current;
			const session = current?.session;
			if (!session || session.state !== "stopped") return;
			setSelectedFrameID(frame.id);
			if (frame.source?.path) {
				onOpenFileRef.current(frame.source.path, frame.line, frame.column);
			}
			if (frame.id === current.frames[0]?.id) {
				setScopes(current.scopes);
				return;
			}
			scopeRequestRef.current?.abort();
			const controller = new AbortController();
			scopeRequestRef.current = controller;
			const stateVersion = session.state_version;
			const endBusy = beginBusy();
			try {
				const result = await track(
					controller,
					getDebugScopes(frame.id, session.session_id, controller.signal),
				);
				if (
					!controller.signal.aborted &&
					selectedFrameRef.current === frame.id &&
					inspectionRef.current?.session?.session_id === session.session_id &&
					inspectionRef.current.session.state_version === stateVersion &&
					inspectionRef.current.session.state === "stopped"
				)
					setScopes(result.scopes);
			} catch (cause) {
				if (!controller.signal.aborted) setError(errorMessage(cause));
			} finally {
				if (scopeRequestRef.current === controller)
					scopeRequestRef.current = null;
				endBusy();
			}
		},
		[beginBusy, track],
	);

	const control = useCallback(
		async (operation: DebugOperation) => {
			const session = inspectionRef.current?.session;
			if (!session || session.state === "terminated") return;
			scopeRequestRef.current?.abort();
			const endBusy = beginBusy();
			setError("");
			try {
				const result = await controlDebug(
					operation,
					session.session_id,
					session.stop?.thread_id,
				);
				if (operation === "stop" && result.session) {
					const current = inspectionRef.current;
					if (current) {
						const terminated = {
							...current,
							session: result.session,
							threads: [],
							frames: [],
							scopes: [],
						};
						inspectionRef.current = terminated;
						setInspection(terminated);
					}
					setSelectedFrameID(undefined);
					setScopes([]);
				} else {
					await refresh();
				}
			} catch (cause) {
				setError(errorMessage(cause));
			} finally {
				endBusy();
			}
		},
		[beginBusy, refresh],
	);

	const session = inspection?.session;
	const stopped = session?.state === "stopped";
	return (
		<div className="flex h-full min-h-0 flex-col bg-bg">
			{session && (
				<div className="flex h-9 shrink-0 items-center gap-1 border-b border-border-subtle bg-bg-surface/30 px-2">
					{session.state !== "terminated" ? (
						<DebugControls
							stopped={stopped}
							supportsStepBack={session.capabilities.supports_step_back}
							busy={busy}
							onControl={(operation) => void control(operation)}
						/>
					) : (
						<button
							type="button"
							onClick={onLaunch}
							className="flex h-6 items-center gap-1 rounded px-1.5 text-[10px] text-fg-muted hover:bg-bg-hover hover:text-fg"
						>
							<Play size={10} /> New session
						</button>
					)}
					<div className="flex-1" />
					<span
						className={`rounded-full px-2 py-0.5 text-[10px] capitalize ${
							stopped
								? "bg-warning/10 text-warning"
								: session.state === "running"
									? "bg-success/10 text-success"
									: "bg-bg-hover text-fg-muted"
						}`}
					>
						{session.state}
					</span>
					<button
						type="button"
						title="Refresh debugger state"
						aria-label="Refresh debugger state"
						onClick={() => void refresh(true)}
						className="flex h-6 w-6 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
					>
						{busy ? (
							<Loader2 size={12} className="animate-spin" />
						) : (
							<RefreshCw size={12} />
						)}
					</button>
				</div>
			)}

			{!session && !inspection?.output ? (
				<div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 text-center">
					<div className="flex h-10 w-10 items-center justify-center rounded-xl border border-border-subtle bg-bg-surface text-fg-dim">
						<Bug size={18} />
					</div>
					<div className="space-y-1">
						<div className="text-[12px] font-medium text-fg">
							No active session
						</div>
						<div className="max-w-56 text-[11px] leading-relaxed text-fg-dim">
							Use Run or Debug above an entry point. Frames, variables, and
							output will appear here.
						</div>
					</div>
				</div>
			) : (
				<div className="grid min-h-0 flex-1 grid-rows-[minmax(100px,0.65fr)_minmax(160px,1.3fr)_minmax(110px,0.7fr)] overflow-hidden">
					<section className="min-h-0 overflow-auto border-b border-border-subtle">
						<SectionTitle count={inspection?.frames.length}>
							Call stack
						</SectionTitle>
						{inspection?.frames.length ? (
							<div className="py-1">
								{inspection.frames.map((frame) => (
									<button
										type="button"
										key={frame.id}
										onClick={() => void selectFrame(frame)}
										className={`block w-full border-l-2 px-3 py-1.5 text-left text-[11px] hover:bg-bg-hover ${
											selectedFrameID === frame.id
												? "border-accent bg-bg-hover text-fg"
												: "border-transparent text-fg-muted"
										}`}
									>
										<div className="truncate">{frame.name}</div>
										{frame.source?.path && (
											<div className="truncate text-[10px] text-fg-dim">
												{frame.source.path}:{frame.line}
											</div>
										)}
									</button>
								))}
							</div>
						) : (
							<EmptyDetail>
								{stopped ? "No stack frames" : "Available while stopped"}
							</EmptyDetail>
						)}
					</section>

					<section className="min-h-0 overflow-auto border-b border-border-subtle">
						<SectionTitle>Variables</SectionTitle>
						{scopes.length ? (
							<div className="pb-2">
								{scopes.map((scope, index) => (
									<ScopeView
										key={`${session?.session_id}:${session?.state_version}:${scope.scope.name}:${index}`}
										inspection={scope}
										sessionID={session?.session_id}
									/>
								))}
							</div>
						) : (
							<EmptyDetail>
								{stopped ? "No variables" : "Available while stopped"}
							</EmptyDetail>
						)}
					</section>

					<section className="flex min-h-0 flex-col bg-black/10">
						<SectionTitle>Debug output</SectionTitle>
						<pre
							ref={outputRef}
							className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words px-3 py-2 font-mono text-[11px] leading-relaxed text-fg-muted select-text"
						>
							{inspection?.output || "No debugger output yet."}
						</pre>
					</section>
				</div>
			)}

			{error && (
				<div className="shrink-0 border-t border-danger/30 bg-danger/5 px-3 py-2 text-[11px] text-danger">
					{error}
				</div>
			)}
		</div>
	);
}

function DebugControls({
	stopped,
	supportsStepBack,
	busy,
	onControl,
}: {
	stopped: boolean;
	supportsStepBack: boolean;
	busy: boolean;
	onControl: (operation: DebugOperation) => void;
}) {
	return (
		<div className="flex items-center gap-1">
			{stopped ? (
				<>
					{supportsStepBack && (
						<Control
							label="Step back"
							disabled={busy}
							onClick={() => onControl("stepBack")}
						>
							<StepBack size={12} />
						</Control>
					)}
					<Control
						label="Continue"
						disabled={busy}
						onClick={() => onControl("continue")}
					>
						<Play size={12} />
					</Control>
					<Control
						label="Step over"
						disabled={busy}
						onClick={() => onControl("next")}
					>
						<RedoDot size={12} />
					</Control>
					<Control
						label="Step into"
						disabled={busy}
						onClick={() => onControl("stepIn")}
					>
						<ArrowDownToDot size={12} />
					</Control>
					<Control
						label="Step out"
						disabled={busy}
						onClick={() => onControl("stepOut")}
					>
						<UndoDot size={12} />
					</Control>
				</>
			) : (
				<Control
					label="Pause"
					disabled={busy}
					onClick={() => onControl("pause")}
				>
					<Pause size={11} />
				</Control>
			)}
			<Control label="Stop" disabled={busy} onClick={() => onControl("stop")}>
				<Square size={10} />
			</Control>
		</div>
	);
}

function Control({
	label,
	disabled,
	onClick,
	children,
}: {
	label: string;
	disabled: boolean;
	onClick: () => void;
	children: ReactNode;
}) {
	return (
		<button
			type="button"
			disabled={disabled}
			title={label}
			aria-label={label}
			onClick={onClick}
			className="flex h-6 min-w-6 items-center justify-center rounded px-1.5 text-[10px] text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-40"
		>
			{children}
		</button>
	);
}

function ScopeView({
	inspection,
	sessionID,
}: {
	inspection: DebugScopeInspection;
	sessionID?: string;
}) {
	return (
		<div className="border-b border-border-subtle last:border-b-0">
			<div className="sticky top-7 z-10 bg-bg-surface/90 px-3 py-1.5 text-[10px] font-medium uppercase tracking-wide text-fg-dim backdrop-blur">
				{inspection.scope.name || "Scope"}
			</div>
			{inspection.error ? (
				<div className="px-3 py-2 text-[11px] text-danger">
					{inspection.error}
				</div>
			) : inspection.variables.length ? (
				inspection.variables.map((variable, index) => (
					<VariableRow
						key={`${variable.name}:${index}`}
						variable={variable}
						sessionID={sessionID}
						depth={0}
					/>
				))
			) : (
				<div className="px-3 py-2 text-[11px] text-fg-dim">Empty</div>
			)}
		</div>
	);
}

function VariableRow({
	variable,
	sessionID,
	depth,
}: {
	variable: DebugVariable;
	sessionID?: string;
	depth: number;
}) {
	const [expanded, setExpanded] = useState(false);
	const [children, setChildren] = useState<DebugVariable[] | null>(null);
	const [loading, setLoading] = useState(false);
	const [error, setError] = useState("");
	const controllerRef = useRef<AbortController | null>(null);
	const reference = variable.variables_reference ?? 0;
	useEffect(() => () => controllerRef.current?.abort(), []);

	const toggle = async () => {
		if (reference <= 0) return;
		if (expanded) {
			setExpanded(false);
			return;
		}
		setExpanded(true);
		if (children) return;
		controllerRef.current?.abort();
		const controller = new AbortController();
		controllerRef.current = controller;
		setLoading(true);
		setError("");
		try {
			const result = await getDebugVariables(
				reference,
				sessionID,
				controller.signal,
			);
			if (!controller.signal.aborted) setChildren(result.variables);
		} catch (cause) {
			if (!controller.signal.aborted) setError(errorMessage(cause));
		} finally {
			if (!controller.signal.aborted) setLoading(false);
		}
	};

	return (
		<div>
			<button
				type="button"
				disabled={reference <= 0}
				onClick={() => void toggle()}
				className="grid w-full grid-cols-[minmax(72px,0.65fr)_minmax(92px,1.35fr)] items-start gap-2 px-3 py-1 text-left text-[11px] hover:bg-bg-hover disabled:hover:bg-transparent"
				style={{ paddingLeft: `${12 + depth * 12}px` }}
			>
				<span className="flex min-w-0 items-center gap-1 text-fg-muted">
					<span className="flex h-3 w-3 shrink-0 items-center justify-center text-fg-dim">
						{loading ? (
							<Loader2 size={10} className="animate-spin" />
						) : reference > 0 ? (
							expanded ? (
								<ChevronDown size={10} />
							) : (
								<ChevronRight size={10} />
							)
						) : null}
					</span>
					<span className="truncate" title={variable.name}>
						{variable.name}
					</span>
				</span>
				<span className="min-w-0 break-words font-mono text-fg">
					{variable.value}
					{variable.type && (
						<span className="ml-1 font-sans text-[10px] text-fg-dim">
							{variable.type}
						</span>
					)}
				</span>
			</button>
			{expanded && error && (
				<div
					className="py-1 pr-3 text-[10px] text-danger"
					style={{ paddingLeft: `${28 + depth * 12}px` }}
				>
					{error}
				</div>
			)}
			{expanded &&
				children?.map((child, index) => (
					<VariableRow
						key={`${child.name}:${index}`}
						variable={child}
						sessionID={sessionID}
						depth={depth + 1}
					/>
				))}
		</div>
	);
}

function SectionTitle({
	children,
	count,
}: {
	children: ReactNode;
	count?: number;
}) {
	return (
		<div className="sticky top-0 z-20 flex h-7 items-center border-b border-border-subtle bg-bg-surface/95 px-3 text-[10px] font-medium uppercase tracking-wide text-fg-dim backdrop-blur">
			{children}
			{count !== undefined && (
				<span className="ml-auto font-mono text-[9px] text-fg-dim">
					{count}
				</span>
			)}
		</div>
	);
}

function EmptyDetail({ children }: { children: ReactNode }) {
	return <div className="px-3 py-4 text-[11px] text-fg-dim">{children}</div>;
}

function errorMessage(value: unknown) {
	return value instanceof Error ? value.message : String(value);
}
