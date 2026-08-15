import { useCallback, useEffect, useRef, useState } from "react";
import type {
	ClientMessage,
	ConversationMessage,
	Phase,
	PromptAction,
	PromptField,
	PromptKind,
	ServerMessage,
	TurnInputIntent,
	TurnInputState,
	TurnQueueEntry,
} from "../types/protocol";
import { discardUncommittedStreamEntries } from "../streamEntries";

export interface PendingPrompt {
	id: string;
	kind: PromptKind;
	message: string;
	fields?: PromptField[];
}

export interface PromptReply {
	text?: string;
	approved?: boolean;
	always?: boolean;
	action?: PromptAction;
	content?: Record<string, unknown>;
}

export interface ChatEntry {
	id: string;
	type: "user" | "assistant" | "tool" | "reasoning";
	content: string;
	// Stable source identity; id above is the local React/rendering identity.
	messageId?: string;
	images?: string[];
	files?: string[];
	inputId?: string;
	toolName?: string;
	toolArgs?: string;
	toolHint?: string;
	toolResult?: string;
	toolId?: string;
	toolPartial?: boolean;
	reasoningId?: string;
}

export function messagesToEntries(
	messages: ConversationMessage[],
): ChatEntry[] {
	const entries: ChatEntry[] = [];
	messages.forEach((m, mi) => {
		const isUser = m.role === "user";
		if (isUser) {
			const text: string[] = [];
			const files: string[] = [];
			const images: string[] = [];
			for (const c of m.content) {
				if (c.text) {
					const file = c.text.match(/^\[File: ([^\n]+)\]$/);
					if (file) files.push(file[1]);
					else text.push(c.text);
				}
				if (c.image?.data) images.push(c.image.data);
			}
			if (text.length > 0 || files.length > 0 || images.length > 0) {
				entries.push({
					id: `u-${mi}`,
					type: "user",
					content: text.join("\n"),
					files,
					images,
				});
			}
			return;
		}

		m.content.forEach((c, ci) => {
			if (c.text?.trim()) {
				entries.push({
					id: `t-${mi}-${ci}`,
					type: "assistant",
					content: c.text,
					messageId: c.text_id,
				});
			}
			if (c.reasoning?.summary) {
				entries.push({
					id: `r-${mi}-${ci}`,
					type: "reasoning",
					content: c.reasoning.summary,
					reasoningId: c.reasoning.id,
				});
			}
			if (c.tool_call) {
				entries.push({
					id: `tc-${mi}-${ci}`,
					type: "tool",
					content: "",
					toolId: c.tool_call.id,
					toolName: c.tool_call.name,
					toolArgs: c.tool_call.args,
					toolHint: c.tool_call.hint,
				});
			}
			if (c.tool_result) {
				const r = c.tool_result;
				const existing = r.id
					? entries.findLast((e) => e.type === "tool" && e.toolId === r.id)
					: entries.findLast(
							(e) =>
								e.type === "tool" &&
								e.toolName === r.name &&
								e.toolResult === undefined,
						);
				if (existing) {
					existing.toolResult = r.content;
				} else {
					entries.push({
						id: `tr-${mi}-${ci}`,
						type: "tool",
						content: "",
						toolId: r.id,
						toolName: r.name,
						toolArgs: r.args,
						toolResult: r.content,
					});
				}
			}
		});
	});
	return entries;
}

interface Usage {
	inputTokens: number;
	cachedTokens: number;
	outputTokens: number;
	lastInputTokens: number;
	contextWindow: number;
}

export interface SessionState {
	id: string;
	entries: ChatEntry[];
	error: string | null;
	phase: Phase;
	usage: Usage;
	prompt: PendingPrompt | null;
	pendingInputs: PendingTurnInput[];
	queuePaused: boolean;
	canSteer: boolean;
}

export interface PendingTurnInput {
	id: string;
	state: TurnInputState;
	intent: TurnInputIntent;
	position: number;
	text: string;
	files: string[];
	images: string[];
	error?: string;
}

const EMPTY_USAGE: Usage = {
	inputTokens: 0,
	cachedTokens: 0,
	outputTokens: 0,
	lastInputTokens: 0,
	contextWindow: 0,
};

function emptySession(id: string): SessionState {
	return {
		id,
		entries: [],
		error: null,
		phase: "idle",
		usage: EMPTY_USAGE,
		prompt: null,
		pendingInputs: [],
		queuePaused: false,
		canSteer: false,
	};
}

function pendingInputFromEntry(
	entry: TurnQueueEntry,
	error?: string,
): PendingTurnInput {
	return {
		id: entry.id,
		state: entry.state,
		intent: entry.intent ?? "follow_up",
		position: entry.position ?? 0,
		text: entry.text ?? "",
		files: entry.files ?? [],
		images: entry.images ?? [],
		error,
	};
}

function upsertPending(
	inputs: PendingTurnInput[],
	input: PendingTurnInput,
): PendingTurnInput[] {
	const idx = inputs.findIndex((item) => item.id === input.id);
	if (idx < 0) return [...inputs, input];
	const next = [...inputs];
	next[idx] = input;
	return next;
}

function ensureUserEntry(
	entries: ChatEntry[],
	input: PendingTurnInput,
	allowContentMatch = false,
): ChatEntry[] {
	if (entries.some((entry) => entry.inputId === input.id)) return entries;
	if (allowContentMatch) {
		const matching = entries.findLast(
			(entry) =>
				entry.type === "user" &&
				entry.content === input.text &&
				(entry.images?.length ?? 0) === input.images.length &&
				(entry.files?.length ?? 0) === input.files.length,
		);
		if (matching) return entries;
	}
	return [
		...entries,
		{
			id: `input-${input.id}`,
			type: "user",
			content: input.text,
			images: input.images,
			files: input.files,
			inputId: input.id,
		},
	];
}

interface StreamRefs {
	textEntryId: string;
	textMessageId: string;
	textContent: string;
	reasoningEntryId: string;
	reasoningId: string;
	reasoningPart: number;
	reasoningContent: string;
	liveEntryIds: Set<string>;
	attemptEntryIds: Set<string>;
}

function emptyStreamRefs(): StreamRefs {
	return {
		textEntryId: "",
		textMessageId: "",
		textContent: "",
		reasoningEntryId: "",
		reasoningId: "",
		reasoningPart: 0,
		reasoningContent: "",
		liveEntryIds: new Set(),
		attemptEntryIds: new Set(),
	};
}

function forgetPartialEntries(entries: ChatEntry[], liveEntryIds: Set<string>) {
	for (const entry of entries) {
		if (entry.toolPartial) liveEntryIds.delete(entry.id);
	}
}

function reconcileSnapshotEntries(
	snapshotEntries: ChatEntry[],
	previousEntries: ChatEntry[],
	liveEntryIds: ReadonlySet<string>,
): { entries: ChatEntry[]; remaining: Set<string> } {
	const live = previousEntries.filter((entry) => liveEntryIds.has(entry.id));
	const remaining = new Set<string>();
	const entries = [...snapshotEntries];
	const tailStart = entries.findLastIndex((entry) => entry.type === "user");
	const committedTail = entries.slice(Math.max(tailStart, 0));

	for (const entry of live) {
		let represented = false;
		if (entry.type === "tool" && entry.toolId) {
			const tailIdx = committedTail.findLastIndex(
				(candidate) =>
					candidate.type === "tool" && candidate.toolId === entry.toolId,
			);
			if (tailIdx >= 0) {
				const idx = Math.max(tailStart, 0) + tailIdx;
				if (
					entry.toolResult !== undefined &&
					entries[idx].toolResult === undefined
				) {
					entries[idx] = { ...entries[idx], toolResult: entry.toolResult };
				}
				represented = true;
			}
		} else if (entry.type === "reasoning") {
			represented = committedTail.some(
				(candidate) =>
					candidate.type === "reasoning" &&
					((entry.reasoningId && candidate.reasoningId === entry.reasoningId) ||
						candidate.content === entry.content),
			);
		} else if (entry.type === "assistant") {
			represented = committedTail.some(
				(candidate) =>
					candidate.type === "assistant" &&
					((entry.messageId && candidate.messageId === entry.messageId) ||
						candidate.content === entry.content),
			);
		}

		if (!represented) {
			entries.push(entry);
			remaining.add(entry.id);
		}
	}

	return { entries, remaining };
}

export function useWebSocket() {
	const wsRef = useRef<WebSocket | null>(null);
	const [connected, setConnected] = useState(false);
	const [sessions, setSessionsState] = useState<Record<string, SessionState>>(
		{},
	);
	const [toolProgress, setToolProgress] = useState<Record<string, string>>({});

	const sessionsSnapshotRef = useRef(sessions);
	// Compute eagerly and pass React a value: updater functions may be invoked
	// more than once in StrictMode, while this projection must run exactly once.
	const commitSessions = useCallback(
		(
			fn: (
				sessions: Record<string, SessionState>,
			) => Record<string, SessionState>,
		) => {
			const previous = sessionsSnapshotRef.current;
			const next = fn(previous);
			if (next === previous) return;
			sessionsSnapshotRef.current = next;
			setSessionsState(next);
		},
		[],
	);
	const hasSession = useCallback(
		(id: string) => id in sessionsSnapshotRef.current,
		[],
	);

	const streamRefs = useRef<Record<string, StreamRefs>>({});
	const pendingFlushSessions = useRef<Set<string>>(new Set());
	const flushFrameRef = useRef<number | null>(null);

	const subscribersRef = useRef<Set<(msg: ServerMessage) => void>>(new Set());

	const subscribe = useCallback((handler: (msg: ServerMessage) => void) => {
		subscribersRef.current.add(handler);
		return () => {
			subscribersRef.current.delete(handler);
		};
	}, []);

	const nextId = () => crypto.randomUUID();

	const getStream = (id: string): StreamRefs => {
		let s = streamRefs.current[id];
		if (!s) {
			s = emptyStreamRefs();
			streamRefs.current[id] = s;
		}
		return s;
	};

	const updateSession = useCallback(
		(id: string, fn: (s: SessionState) => SessionState) => {
			commitSessions((prev) => {
				const existing = prev[id];
				const base = existing ?? emptySession(id);
				const updated = fn(base);
				if (existing && updated === existing) return prev;
				return { ...prev, [id]: updated };
			});
		},
		[commitSessions],
	);

	const flushStreamSession = useCallback(
		(id: string) => {
			pendingFlushSessions.current.delete(id);
			const s = streamRefs.current[id];
			if (!s) return;

			const streaming =
				s.textEntryId && s.textContent
					? {
							id: s.textEntryId,
							messageId: s.textMessageId,
							content: s.textContent,
						}
					: null;
			const reasoning =
				s.reasoningEntryId && s.reasoningContent
					? {
							id: s.reasoningEntryId,
							content: s.reasoningContent,
							reasoningId: s.reasoningId,
						}
					: null;
			if (!streaming && !reasoning) return;

			updateSession(id, (sess) => {
				let entries = sess.entries;
				let changed = false;

				const upsert = (entry: ChatEntry) => {
					const idx = entries.findIndex((e) => e.id === entry.id);
					if (idx < 0) {
						entries = [...entries, entry];
						changed = true;
						return;
					}

					const existing = entries[idx];
					if (
						existing.content === entry.content &&
						existing.reasoningId === entry.reasoningId
					) {
						return;
					}

					const next = entries === sess.entries ? [...entries] : entries;
					next[idx] = { ...existing, ...entry };
					entries = next;
					changed = true;
				};

				if (reasoning) {
					upsert({
						id: reasoning.id,
						type: "reasoning",
						content: reasoning.content,
						reasoningId: reasoning.reasoningId,
					});
				}
				if (streaming) {
					upsert({
						id: streaming.id,
						type: "assistant",
						content: streaming.content,
						messageId: streaming.messageId,
					});
				}

				return changed ? { ...sess, entries } : sess;
			});
		},
		[updateSession],
	);

	const flushPendingStreams = useCallback(() => {
		flushFrameRef.current = null;
		const ids = Array.from(pendingFlushSessions.current);
		pendingFlushSessions.current.clear();
		for (const id of ids) flushStreamSession(id);
	}, [flushStreamSession]);

	const scheduleStreamFlush = useCallback(
		(id: string) => {
			pendingFlushSessions.current.add(id);
			if (flushFrameRef.current !== null) return;
			flushFrameRef.current = window.requestAnimationFrame(flushPendingStreams);
		},
		[flushPendingStreams],
	);

	const flushActiveStream = useCallback(
		(id: string) => {
			const s = streamRefs.current[id];
			if (!s?.textEntryId && !s?.reasoningEntryId) return;
			flushStreamSession(id);
		},
		[flushStreamSession],
	);

	const finalizeStreaming = (id: string) => {
		const s = getStream(id);
		s.textEntryId = "";
		s.textMessageId = "";
		s.textContent = "";
	};

	const finalizeReasoning = (id: string) => {
		const s = getStream(id);
		s.reasoningEntryId = "";
		s.reasoningId = "";
		s.reasoningPart = 0;
		s.reasoningContent = "";
	};

	const handleMessageRef = useRef<(msg: ServerMessage) => void>(() => {});

	const handleMessage = (msg: ServerMessage) => {
		const sid = "session" in msg ? msg.session : undefined;

		for (const sub of subscribersRef.current) {
			sub(msg);
		}

		if (msg.type === "tool_progress") {
			setToolProgress((prev) => {
				if (msg.text) return { ...prev, [msg.id]: msg.text };
				if (!(msg.id in prev)) return prev;
				const rest = { ...prev };
				delete rest[msg.id];
				return rest;
			});
			return;
		}

		if (!sid) return;

		switch (msg.type) {
			case "session_state": {
				flushActiveStream(sid);
				pendingFlushSessions.current.delete(sid);
				const snapshotEntries = messagesToEntries(msg.messages ?? []);
				const stream = getStream(sid);
				const previous = sessionsSnapshotRef.current[sid];
				const { entries, remaining } = reconcileSnapshotEntries(
					snapshotEntries,
					previous?.entries ?? [],
					stream.liveEntryIds,
				);

				stream.liveEntryIds = remaining;
				stream.attemptEntryIds = new Set(
					Array.from(stream.attemptEntryIds).filter((id) => remaining.has(id)),
				);
				if (stream.textEntryId && !remaining.has(stream.textEntryId)) {
					finalizeStreaming(sid);
				}
				if (
					stream.reasoningEntryId &&
					!remaining.has(stream.reasoningEntryId)
				) {
					finalizeReasoning(sid);
				}

				commitSessions((prev) => ({
					...prev,
					[sid]: {
						id: sid,
						entries,
						error: prev[sid]?.error ?? null,
						phase: msg.phase ?? "idle",
						usage: {
							inputTokens: msg.input_tokens ?? 0,
							cachedTokens: msg.cached_tokens ?? 0,
							outputTokens: msg.output_tokens ?? 0,
							lastInputTokens: msg.last_input_tokens ?? 0,
							contextWindow: msg.context_window ?? 0,
						},
						prompt: prev[sid]?.prompt ?? null,
						pendingInputs: prev[sid]?.pendingInputs ?? [],
						queuePaused: prev[sid]?.queuePaused ?? false,
						canSteer: prev[sid]?.canSteer ?? false,
					},
				}));
				break;
			}

			case "text_delta": {
				if (streamRefs.current[sid]?.reasoningEntryId) {
					flushStreamSession(sid);
				}
				finalizeReasoning(sid);
				const s = getStream(sid);
				if (
					s.textEntryId &&
					msg.id &&
					s.textMessageId &&
					msg.id !== s.textMessageId
				) {
					flushStreamSession(sid);
					finalizeStreaming(sid);
				}
				let entryId = s.textEntryId;
				if (!entryId) {
					entryId = nextId();
					s.textEntryId = entryId;
					s.textMessageId = msg.id ?? "";
					s.liveEntryIds.add(entryId);
					s.attemptEntryIds.add(entryId);
				}
				s.textContent += msg.text;
				scheduleStreamFlush(sid);
				break;
			}

			case "reasoning_delta": {
				if (streamRefs.current[sid]?.textEntryId) {
					flushStreamSession(sid);
				}
				finalizeStreaming(sid);
				const s = getStream(sid);
				if (s.reasoningEntryId && s.reasoningId !== msg.id) {
					flushStreamSession(sid);
					finalizeReasoning(sid);
				}
				let entryId = s.reasoningEntryId;
				if (!entryId) {
					entryId = nextId();
					s.reasoningEntryId = entryId;
					s.reasoningId = msg.id;
					s.liveEntryIds.add(entryId);
					s.attemptEntryIds.add(entryId);
				}
				const part = msg.part ?? 0;
				if (s.reasoningContent && part !== s.reasoningPart) {
					s.reasoningContent += "\n\n";
				}
				s.reasoningPart = part;
				s.reasoningContent += msg.text;
				scheduleStreamFlush(sid);
				break;
			}

			case "tool_call": {
				flushActiveStream(sid);
				finalizeStreaming(sid);
				finalizeReasoning(sid);
				const stream = getStream(sid);
				updateSession(sid, (sess) => {
					const idx = sess.entries.findLastIndex(
						(entry) => entry.type === "tool" && entry.toolId === msg.id,
					);
					if (idx >= 0) {
						const entries = [...sess.entries];
						entries[idx] = {
							...entries[idx],
							toolName: msg.name,
							toolArgs: msg.args,
							toolHint: msg.hint,
							toolPartial: msg.partial,
						};
						stream.liveEntryIds.add(entries[idx].id);
						return { ...sess, entries };
					}
					const id = nextId();
					stream.liveEntryIds.add(id);
					return {
						...sess,
						entries: [
							...sess.entries,
							{
								id,
								type: "tool",
								content: "",
								toolId: msg.id,
								toolName: msg.name,
								toolArgs: msg.args,
								toolHint: msg.hint,
								toolPartial: msg.partial,
							},
						],
					};
				});
				break;
			}

			case "tool_result": {
				const stream = getStream(sid);
				setToolProgress((prev) => {
					if (!(msg.id in prev)) return prev;
					const rest = { ...prev };
					delete rest[msg.id];
					return rest;
				});
				updateSession(sid, (sess) => {
					const idx = msg.id
						? sess.entries.findLastIndex(
								(e) => e.type === "tool" && e.toolId === msg.id,
							)
						: sess.entries.findLastIndex(
								(e) =>
									e.type === "tool" &&
									e.toolName === msg.name &&
									e.toolResult === undefined,
							);
					if (idx < 0) {
						const id = nextId();
						stream.liveEntryIds.add(id);
						return {
							...sess,
							entries: [
								...sess.entries,
								{
									id,
									type: "tool",
									content: "",
									toolId: msg.id,
									toolName: msg.name,
									toolResult: msg.content,
								},
							],
						};
					}
					const updated = [...sess.entries];
					updated[idx] = {
						...updated[idx],
						toolResult: msg.content,
						toolPartial: false,
					};
					return { ...sess, entries: updated };
				});
				break;
			}

			case "stream_reset": {
				flushActiveStream(sid);
				const stream = getStream(sid);
				const failed = new Set(stream.attemptEntryIds);
				updateSession(sid, (sess) => {
					forgetPartialEntries(sess.entries, stream.liveEntryIds);
					return {
						...sess,
						entries: discardUncommittedStreamEntries(sess.entries, failed),
					};
				});
				for (const id of failed) stream.liveEntryIds.delete(id);
				stream.attemptEntryIds.clear();
				finalizeStreaming(sid);
				finalizeReasoning(sid);
				break;
			}

			case "stream_commit": {
				flushActiveStream(sid);
				const stream = getStream(sid);
				updateSession(sid, (sess) => {
					forgetPartialEntries(sess.entries, stream.liveEntryIds);
					return {
						...sess,
						entries: discardUncommittedStreamEntries(sess.entries),
					};
				});
				stream.attemptEntryIds.clear();
				finalizeStreaming(sid);
				finalizeReasoning(sid);
				break;
			}

			case "phase":
				if (msg.phase === "idle") {
					flushActiveStream(sid);
					finalizeStreaming(sid);
					finalizeReasoning(sid);
					const stream = getStream(sid);
					stream.liveEntryIds.clear();
					stream.attemptEntryIds.clear();
				}
				updateSession(sid, (sess) => ({ ...sess, phase: msg.phase }));
				break;

			case "error": {
				flushActiveStream(sid);
				finalizeStreaming(sid);
				finalizeReasoning(sid);
				const stream = getStream(sid);
				stream.liveEntryIds.clear();
				stream.attemptEntryIds.clear();
				updateSession(sid, (sess) => ({
					...sess,
					entries: discardUncommittedStreamEntries(sess.entries),
					error: msg.message,
				}));
				break;
			}

			case "usage":
				updateSession(sid, (sess) => ({
					...sess,
					usage: {
						inputTokens: msg.input_tokens ?? 0,
						cachedTokens: msg.cached_tokens ?? 0,
						outputTokens: msg.output_tokens ?? 0,
						lastInputTokens: msg.last_input_tokens ?? 0,
						contextWindow: msg.context_window ?? 0,
					},
				}));
				break;

			case "prompt":
				updateSession(sid, (sess) => ({
					...sess,
					prompt: {
						id: msg.prompt_id,
						kind: msg.prompt_kind,
						message: msg.message,
						fields: msg.prompt_fields,
					},
				}));
				break;

			case "prompt_cancel":
				updateSession(sid, (sess) =>
					sess.prompt?.id === msg.prompt_id ? { ...sess, prompt: null } : sess,
				);
				break;

			case "turn_input": {
				const raw: TurnQueueEntry = msg.queue?.[0] ?? {
					id: msg.id,
					state: msg.state,
					intent: msg.intent,
					position: msg.position,
					text: msg.text,
				};
				const input = pendingInputFromEntry(raw, msg.message);
				updateSession(sid, (sess) => {
					let entries = sess.entries;
					let pendingInputs = sess.pendingInputs;
					if (msg.state === "active" || msg.state === "steered") {
						entries = ensureUserEntry(entries, input);
						pendingInputs = pendingInputs.filter((item) => item.id !== msg.id);
					} else if (msg.state === "queued" || msg.state === "sending") {
						pendingInputs = upsertPending(pendingInputs, input);
					} else if (msg.state === "cancelled" || msg.state === "failed") {
						const wasVisible = entries.some(
							(entry) => entry.inputId === msg.id,
						);
						pendingInputs = wasVisible
							? pendingInputs.filter((item) => item.id !== msg.id)
							: upsertPending(pendingInputs, input);
					} else {
						pendingInputs = pendingInputs.filter((item) => item.id !== msg.id);
					}
					return { ...sess, entries, pendingInputs };
				});
				break;
			}

			case "turn_queue": {
				const live = (msg.queue ?? []).map((entry) =>
					pendingInputFromEntry(entry),
				);
				const liveIDs = new Set(live.map((entry) => entry.id));
				updateSession(sid, (sess) => {
					let entries = sess.entries;
					for (const input of live) {
						if (input.state === "active" || input.state === "steered") {
							entries = ensureUserEntry(entries, input, true);
						}
					}
					const local = sess.pendingInputs.filter(
						(input) => !liveIDs.has(input.id) && input.state !== "queued",
					);
					return {
						...sess,
						entries,
						pendingInputs: [
							...live.filter((input) => input.state === "queued"),
							...local,
						],
						queuePaused: msg.paused ?? false,
						canSteer: msg.can_steer ?? false,
					};
				});
				break;
			}
		}
	};

	useEffect(() => {
		handleMessageRef.current = handleMessage;
	});

	const send = useCallback((msg: ClientMessage): boolean => {
		if (wsRef.current?.readyState !== WebSocket.OPEN) {
			return false;
		}
		wsRef.current.send(JSON.stringify(msg));
		return true;
	}, []);

	const sendChat = useCallback(
		(
			sessionId: string,
			text: string,
			files?: string[],
			images?: string[],
			intent: TurnInputIntent = "follow_up",
		): boolean => {
			flushActiveStream(sessionId);
			const id = nextId();
			const sent = send({
				type: "send",
				session: sessionId,
				id,
				intent,
				text,
				files,
				images,
			});
			if (!sent) return false;
			if (intent === "steer") {
				streamRefs.current[sessionId] = emptyStreamRefs();
			}
			updateSession(sessionId, (sess) => ({
				...sess,
				error: null,
				pendingInputs: upsertPending(sess.pendingInputs, {
					id,
					state: "sending",
					intent,
					position: 0,
					text,
					files: files ?? [],
					images: images ?? [],
				}),
			}));
			return true;
		},
		[flushActiveStream, send, updateSession],
	);

	const cancel = useCallback(
		(sessionId: string, clearQueue = false) => {
			send({ type: "cancel", session: sessionId, clear_queue: clearQueue });
		},
		[send],
	);

	const removeQueued = useCallback(
		(sessionId: string, id: string) =>
			send({ type: "queue_remove", session: sessionId, id }),
		[send],
	);

	const updateQueued = useCallback(
		(
			sessionId: string,
			id: string,
			text: string,
			files?: string[],
			images?: string[],
			intent: TurnInputIntent = "follow_up",
		) =>
			send({
				type: "queue_update",
				session: sessionId,
				id,
				text,
				files,
				images,
				intent,
			}),
		[send],
	);

	const resumeQueue = useCallback(
		(sessionId: string) => send({ type: "queue_resume", session: sessionId }),
		[send],
	);

	const clearQueue = useCallback(
		(sessionId: string) => send({ type: "queue_clear", session: sessionId }),
		[send],
	);

	const dismissPending = useCallback(
		(sessionId: string, id: string) => {
			updateSession(sessionId, (sess) => ({
				...sess,
				pendingInputs: sess.pendingInputs.filter((input) => input.id !== id),
			}));
		},
		[updateSession],
	);

	const dismissError = useCallback(
		(sessionId: string) => {
			updateSession(sessionId, (sess) =>
				sess.error ? { ...sess, error: null } : sess,
			);
		},
		[updateSession],
	);

	useEffect(() => {
		const onFocus = () => send({ type: "focus" });
		window.addEventListener("focus", onFocus);
		return () => window.removeEventListener("focus", onFocus);
	}, [send]);

	const respondPrompt = useCallback(
		(sessionId: string, promptId: string, reply: PromptReply) => {
			const sent = send({
				type: "prompt_response",
				session: sessionId,
				prompt_id: promptId,
				text: reply.text,
				approved: reply.approved,
				always: reply.always,
				action: reply.action,
				content: reply.content,
			});
			if (!sent) return;
			updateSession(sessionId, (sess) =>
				sess.prompt?.id === promptId ? { ...sess, prompt: null } : sess,
			);
		},
		[send, updateSession],
	);

	const removeSession = useCallback(
		(sessionId: string) => {
			commitSessions((prev) => {
				if (!(sessionId in prev)) return prev;
				const rest = { ...prev };
				delete rest[sessionId];
				return rest;
			});
			delete streamRefs.current[sessionId];
			pendingFlushSessions.current.delete(sessionId);
		},
		[commitSessions],
	);

	const clearSessions = useCallback(() => {
		commitSessions(() => ({}));
		streamRefs.current = {};
		pendingFlushSessions.current.clear();
	}, [commitSessions]);

	useEffect(() => {
		return () => {
			if (flushFrameRef.current !== null) {
				window.cancelAnimationFrame(flushFrameRef.current);
			}
		};
	}, []);

	useEffect(() => {
		let reconnectTimer: ReturnType<typeof setTimeout>;
		let alive = true;
		let cachedURL: string | null = null;

		async function resolveURL(): Promise<string> {
			if (cachedURL) return cachedURL;
			try {
				const r = await fetch("/api/ws");
				if (r.ok) {
					const d = (await r.json()) as { url?: string };
					if (d.url) {
						cachedURL = d.url;
						return cachedURL;
					}
				}
			} catch {}
			const proto = location.protocol === "https:" ? "wss:" : "ws:";
			cachedURL = `${proto}//${location.host}/ws`;
			return cachedURL;
		}

		async function connect() {
			if (!alive) return;

			const url = await resolveURL();
			if (!alive) return;

			const ws = new WebSocket(url);

			ws.onopen = () => {
				setConnected(true);
				ws.send(
					JSON.stringify({
						type: "sync",
						sessions: Object.keys(sessionsSnapshotRef.current),
					}),
				);
				for (const sub of subscribersRef.current) {
					sub({ type: "diffs_changed" });
					sub({ type: "sessions_changed" });
					sub({ type: "files_changed" });
					sub({ type: "diagnostics_changed" });
					sub({ type: "capabilities_changed" });
				}
			};

			ws.onclose = () => {
				setConnected(false);
				wsRef.current = null;
				commitSessions((prev) => {
					const next: Record<string, SessionState> = {};
					for (const [id, sess] of Object.entries(prev)) {
						next[id] = {
							...sess,
							pendingInputs: sess.pendingInputs.map((input) =>
								input.state === "sending"
									? {
											...input,
											state: "failed",
											error:
												"Connection was lost before delivery was confirmed.",
										}
									: input,
							),
						};
					}
					return next;
				});
				if (alive) {
					reconnectTimer = setTimeout(connect, 2000);
				}
			};

			ws.onerror = () => ws.close();

			ws.onmessage = (e) => {
				const msg: ServerMessage = JSON.parse(e.data);
				handleMessageRef.current(msg);
			};

			wsRef.current = ws;
		}

		connect();

		return () => {
			alive = false;
			clearTimeout(reconnectTimer);
			wsRef.current?.close();
		};
	}, [commitSessions]);

	return {
		connected,
		sessions,
		toolProgress,
		hasSession,
		sendChat,
		cancel,
		removeQueued,
		updateQueued,
		resumeQueue,
		clearQueue,
		dismissPending,
		dismissError,
		respondPrompt,
		removeSession,
		clearSessions,
		subscribe,
	};
}
