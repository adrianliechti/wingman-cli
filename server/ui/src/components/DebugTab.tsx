import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, Loader2 } from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import {
	getDebugInspection,
	getDebugScopes,
	getDebugVariables,
	type DebugInspection,
	type DebugScopeInspection,
	type DebugStackFrame,
	type DebugVariable,
} from "../api/debug";
import { queryKeys } from "../api/query";

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
	const inspectionQuery = useQuery<DebugInspection>({
		queryKey: queryKeys.debug.inspection,
		staleTime: 0,
		queryFn: ({ signal }) => getDebugInspection(signal),
		refetchInterval: (current) =>
			current.state.data?.session?.state === "running" ? 600 : 1_500,
		structuralSharing: (previous, next) =>
			preserveInspection(previous, next as DebugInspection),
	});
	const inspection = inspectionQuery.data ?? null;
	const session = inspection?.session;
	const stopped = session?.state === "stopped";
	const firstFrame = inspection?.frames[0];
	const stopVersion =
		stopped && firstFrame
			? `${session.session_id}:${session.state_version}`
			: "";
	const initialFrameID = stopVersion ? firstFrame?.id : undefined;
	const [frameSelection, setFrameSelection] = useState<{
		stopVersion: string;
		frameID?: number;
	}>({ stopVersion: "" });
	if (frameSelection.stopVersion !== stopVersion) {
		setFrameSelection({ stopVersion, frameID: initialFrameID });
	}
	const selectedFrameID =
		frameSelection.stopVersion === stopVersion
			? frameSelection.frameID
			: initialFrameID;
	const notifiedStopRef = useRef("");
	useEffect(() => {
		if (!stopVersion) {
			notifiedStopRef.current = "";
			return;
		}
		if (notifiedStopRef.current === stopVersion) return;
		notifiedStopRef.current = stopVersion;
		const frame = inspection?.frames[0];
		if (autoOpenSource && frame?.source?.path) {
			onOpenFile(frame.source.path, frame.line, frame.column);
		}
		onStopped?.();
	}, [autoOpenSource, inspection, onOpenFile, onStopped, stopVersion]);

	const scopeSessionID = inspection?.session?.session_id;
	const scopeSessionState = inspection?.session?.state;
	const scopeStateVersion = inspection?.session?.state_version;
	const canLoadScopes =
		!!scopeSessionID &&
		scopeSessionState === "stopped" &&
		scopeStateVersion !== undefined &&
		!!selectedFrameID;
	const scopesQuery = useQuery({
		queryKey: queryKeys.debug.scopes(
			scopeSessionID ?? "",
			scopeStateVersion ?? 0,
			selectedFrameID ?? 0,
		),
		enabled: canLoadScopes,
		queryFn: ({ signal }) =>
			getDebugScopes(selectedFrameID ?? 0, scopeSessionID, signal),
	});
	const scopes = scopesQuery.data?.scopes ?? [];
	const scopeLoading = canLoadScopes && scopesQuery.isFetching;
	const scopeError = scopesQuery.error ? errorMessage(scopesQuery.error) : "";
	const error =
		inspectionQuery.data?.session?.error ||
		inspectionQuery.data?.error ||
		(inspectionQuery.error ? errorMessage(inspectionQuery.error) : "");

	const selectFrame = (frame: DebugStackFrame) => {
		if (!session || session.state !== "stopped") return;
		if (frame.id === selectedFrameID) return;
		setFrameSelection({ stopVersion, frameID: frame.id });
		if (frame.source?.path) {
			onOpenFile(frame.source.path, frame.line, frame.column);
		}
	};

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
										stateVersion={session?.state_version}
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
	stateVersion,
	showHeader,
}: {
	inspection: DebugScopeInspection;
	sessionID?: string;
	stateVersion?: number;
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
						stateVersion={stateVersion}
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
	stateVersion,
	depth,
}: {
	variable: DebugVariable;
	sessionID?: string;
	stateVersion?: number;
	depth: number;
}) {
	const [expanded, setExpanded] = useState(false);
	const reference = variable.variables_reference ?? 0;
	const childrenQuery = useQuery({
		queryKey: queryKeys.debug.variables(
			sessionID ?? "",
			stateVersion ?? 0,
			reference,
		),
		enabled: expanded && reference > 0,
		queryFn: ({ signal }) => getDebugVariables(reference, sessionID, signal),
	});
	const children = childrenQuery.data?.variables ?? null;
	const loading = childrenQuery.isFetching;
	const error = childrenQuery.error ? errorMessage(childrenQuery.error) : "";

	const toggle = () => {
		if (reference <= 0) return;
		setExpanded((current) => !current);
	};

	return (
		<div>
			<button
				type="button"
				disabled={reference <= 0}
				onClick={toggle}
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
						stateVersion={stateVersion}
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

function preserveInspection(
	previous: unknown,
	next: DebugInspection,
): DebugInspection {
	const old = previous as DebugInspection | undefined;
	if (!old) return next;
	if (!next.session && old.session?.state === "terminated") return old;
	if (
		!next.session ||
		!old.session ||
		old.session.session_id !== next.session.session_id ||
		next.session.state_version > old.session.state_version
	) {
		return next;
	}

	// Keep the inspector tree stable within one debugger state so expanded
	// variables are not remounted by output-only polling updates.
	if (old.error === next.error && old.session.error === next.session.error) {
		return old;
	}
	return {
		...old,
		error: next.error,
		session: { ...old.session, error: next.session.error },
	};
}
