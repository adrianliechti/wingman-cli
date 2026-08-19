import { ChevronDown, ChevronRight, Loader2 } from "lucide-react";
import {
	type ReactNode,
	useCallback,
	useEffect,
	useRef,
	useState,
} from "react";
import {
	getDebugInspection,
	getDebugScopes,
	getDebugVariables,
	type DebugInspection,
	type DebugScopeInspection,
	type DebugStackFrame,
	type DebugVariable,
} from "../api/debug";

interface Props {
	onOpenFile: (path: string, line: number, column: number) => void;
	onStopped?: () => void;
	autoOpenSource?: boolean;
}

export function DebugTab({
	onOpenFile,
	onStopped,
	autoOpenSource = true,
}: Props) {
	const requestsRef = useRef(new Set<AbortController>());
	const refreshSequenceRef = useRef(0);
	const inspectionRef = useRef<DebugInspection | null>(null);
	const selectedFrameRef = useRef<number | undefined>(undefined);
	const scopeRequestRef = useRef<AbortController | null>(null);
	const stopVersionRef = useRef("");
	const onOpenFileRef = useRef(onOpenFile);
	const onStoppedRef = useRef(onStopped);
	const [inspection, setInspection] = useState<DebugInspection | null>(null);
	const [selectedFrameID, setSelectedFrameID] = useState<number>();
	const [scopes, setScopes] = useState<DebugScopeInspection[]>([]);
	const [scopeLoading, setScopeLoading] = useState(false);
	const [scopeError, setScopeError] = useState("");
	const [error, setError] = useState("");
	selectedFrameRef.current = selectedFrameID;
	onOpenFileRef.current = onOpenFile;
	onStoppedRef.current = onStopped;

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

	const applyInspection = useCallback(
		(next: DebugInspection) => {
			const current = inspectionRef.current;
			if (!next.session && current?.session?.state === "terminated")
				return false;
			if (
				next.session &&
				current?.session?.session_id === next.session.session_id &&
				next.session.state_version < current.session.state_version
			)
				return false;
			if (
				next.session &&
				current?.session?.session_id === next.session.session_id &&
				current.session.state_version === next.session.state_version
			) {
				// Output is rendered in the other Debug view. While the debugger state
				// is unchanged, retain this inspector tree so expanded locals do not get
				// remounted by the polling loop.
				return false;
			}

			inspectionRef.current = next;
			setInspection(next);
			if (next.session?.state !== "stopped" || next.frames.length === 0) {
				stopVersionRef.current = "";
				setSelectedFrameID(undefined);
				setScopes([]);
				setScopeLoading(false);
				setScopeError("");
				return true;
			}
			const stopVersion = `${next.session.session_id}:${next.session.state_version}`;
			if (stopVersion !== stopVersionRef.current) {
				stopVersionRef.current = stopVersion;
				setSelectedFrameID(next.frames[0].id);
				setScopes([]);
				setScopeError("");
				const frame = next.frames[0];
				if (autoOpenSource && frame.source?.path) {
					onOpenFileRef.current(frame.source.path, frame.line, frame.column);
				}
				onStoppedRef.current?.();
				return true;
			}
			const selected =
				next.frames.find((frame) => frame.id === selectedFrameRef.current) ??
				next.frames[0];
			setSelectedFrameID(selected.id);
			return true;
		},
		[autoOpenSource],
	);

	const refresh = useCallback(async () => {
		const sequence = ++refreshSequenceRef.current;
		const controller = new AbortController();
		try {
			const next = await track(
				controller,
				getDebugInspection(controller.signal),
			);
			if (sequence !== refreshSequenceRef.current) return null;
			applyInspection(next);
			setError(next.session?.error || next.error || "");
			return next;
		} catch (cause) {
			if (!controller.signal.aborted) setError(errorMessage(cause));
			return null;
		}
	}, [applyInspection, track]);

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

	const scopeSessionID = inspection?.session?.session_id;
	const scopeSessionState = inspection?.session?.state;
	const scopeStateVersion = inspection?.session?.state_version;
	useEffect(() => {
		if (
			!scopeSessionID ||
			scopeSessionState !== "stopped" ||
			scopeStateVersion === undefined ||
			!selectedFrameID
		) {
			scopeRequestRef.current?.abort();
			scopeRequestRef.current = null;
			setScopeLoading(false);
			return;
		}

		const controller = new AbortController();
		scopeRequestRef.current?.abort();
		scopeRequestRef.current = controller;
		setScopes([]);
		setScopeLoading(true);
		setScopeError("");
		void track(
			controller,
			getDebugScopes(selectedFrameID, scopeSessionID, controller.signal),
		)
			.then((result) => {
				const current = inspectionRef.current?.session;
				if (
					!controller.signal.aborted &&
					current?.session_id === scopeSessionID &&
					current.state_version === scopeStateVersion &&
					current.state === "stopped" &&
					selectedFrameRef.current === selectedFrameID
				)
					setScopes(result.scopes);
			})
			.catch((cause) => {
				if (!controller.signal.aborted) setScopeError(errorMessage(cause));
			})
			.finally(() => {
				if (scopeRequestRef.current === controller) {
					scopeRequestRef.current = null;
					setScopeLoading(false);
				}
			});

		return () => controller.abort();
	}, [
		scopeSessionID,
		scopeSessionState,
		scopeStateVersion,
		selectedFrameID,
		track,
	]);

	const selectFrame = useCallback((frame: DebugStackFrame) => {
		const current = inspectionRef.current;
		const session = current?.session;
		if (!session || session.state !== "stopped") return;
		if (frame.id === selectedFrameRef.current) return;
		setScopes([]);
		setScopeError("");
		setSelectedFrameID(frame.id);
		if (frame.source?.path) {
			onOpenFileRef.current(frame.source.path, frame.line, frame.column);
		}
	}, []);

	const session = inspection?.session;
	const stopped = session?.state === "stopped";
	const soleScope = scopes.length === 1 ? scopes[0] : undefined;
	return (
		<div className="flex h-full min-h-0 flex-col bg-bg">
			{!session ? (
				<div className="flex flex-1 flex-col items-center justify-center gap-2 px-6 text-center">
					<div className="text-[12px] text-fg-muted">No active session</div>
					<div className="max-w-52 text-[10px] leading-relaxed text-fg-dim">
						Choose Run or Debug beside a runnable entry point in the editor.
					</div>
				</div>
			) : (
				<div className="grid min-h-0 flex-1 grid-rows-[minmax(160px,1.3fr)_minmax(100px,0.65fr)] overflow-hidden">
					<section className="min-h-0 overflow-auto border-b border-border-subtle">
						<SectionTitle>{soleScope?.scope.name || "Variables"}</SectionTitle>
						{scopeLoading ? (
							<div className="flex items-center gap-2 px-3 py-4 text-[11px] text-fg-dim">
								<Loader2 size={11} className="animate-spin" /> Loading
								variables…
							</div>
						) : scopeError ? (
							<div className="px-3 py-3 text-[11px] leading-relaxed text-danger">
								{scopeError}
							</div>
						) : scopes.length ? (
							<div className="pb-2">
								{scopes.map((scope, index) => (
									<ScopeView
										key={`${session?.session_id}:${session?.state_version}:${scope.scope.name}:${index}`}
										inspection={scope}
										sessionID={session?.session_id}
										showHeader={!soleScope}
									/>
								))}
							</div>
						) : (
							<EmptyDetail>
								{stopped
									? "No variables in this frame"
									: "Available while stopped"}
							</EmptyDetail>
						)}
					</section>

					<section className="min-h-0 overflow-auto">
						<SectionTitle>Call stack</SectionTitle>
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

function ScopeView({
	inspection,
	sessionID,
	showHeader,
}: {
	inspection: DebugScopeInspection;
	sessionID?: string;
	showHeader: boolean;
}) {
	return (
		<div
			className={
				showHeader ? "border-b border-border-subtle last:border-b-0" : ""
			}
		>
			{showHeader && (
				<div className="sticky top-7 z-10 bg-bg-surface/90 px-3 py-1.5 text-[10px] font-medium uppercase tracking-wide text-fg-dim backdrop-blur">
					{inspection.scope.name || "Scope"}
				</div>
			)}
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

function SectionTitle({ children }: { children: ReactNode }) {
	return (
		<div className="sticky top-0 z-20 flex h-7 items-center border-b border-border-subtle bg-bg-surface/95 px-3 text-[10px] font-medium uppercase tracking-wide text-fg-dim backdrop-blur">
			{children}
		</div>
	);
}

function EmptyDetail({ children }: { children: ReactNode }) {
	return <div className="px-3 py-4 text-[11px] text-fg-dim">{children}</div>;
}

function errorMessage(value: unknown) {
	return value instanceof Error ? value.message : String(value);
}
