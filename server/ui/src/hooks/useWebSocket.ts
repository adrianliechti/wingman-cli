import { useCallback, useEffect, useRef, useState } from "react";
import type {
	ClientMessage,
	ConversationMessage,
	Phase,
	ServerMessage,
} from "../types/protocol";

export interface ChatEntry {
	id: string;
	type: "user" | "assistant" | "tool" | "reasoning" | "error";
	content: string;
	toolName?: string;
	toolArgs?: string;
	toolHint?: string;
	toolResult?: string;
	toolId?: string;
	reasoningId?: string;
}

export function messagesToEntries(messages: ConversationMessage[]): ChatEntry[] {
	const entries: ChatEntry[] = [];
	for (const m of messages) {
		for (const c of m.content) {
			if (c.text) {
				entries.push({
					id: crypto.randomUUID(),
					type: m.role === "user" ? "user" : "assistant",
					content: c.text,
				});
			}
			if (c.reasoning?.summary) {
				entries.push({
					id: crypto.randomUUID(),
					type: "reasoning",
					content: c.reasoning.summary,
					reasoningId: c.reasoning.id,
				});
			}
			if (c.tool_result) {
				entries.push({
					id: crypto.randomUUID(),
					type: "tool",
					content: "",
					toolName: c.tool_result.name,
					toolArgs: c.tool_result.args,
					toolResult: c.tool_result.content,
				});
			}
		}
	}
	return entries;
}

interface Usage {
	inputTokens: number;
	outputTokens: number;
}

export function useWebSocket() {
	const wsRef = useRef<WebSocket | null>(null);
	const [connected, setConnected] = useState(false);
	const [phase, setPhase] = useState<Phase>("idle");
	const [entries, setEntries] = useState<ChatEntry[]>([]);
	const [usage, setUsage] = useState<Usage>({
		inputTokens: 0,
		outputTokens: 0,
	});
	const [prompt, setPrompt] = useState<{
		type: "prompt" | "ask";
		question: string;
	} | null>(null);

	const streamingRef = useRef("");
	const streamingIdRef = useRef("");
	const reasoningRef = useRef("");
	const reasoningIdRef = useRef("");
	const reasoningEntryIdRef = useRef("");

	const nextId = () => crypto.randomUUID();

	// Anyone (e.g. DiffsPanel, CheckpointsPanel) can subscribe to raw server
	// messages. The set is a ref so adding/removing subscribers does not retrigger
	// the WebSocket connection effect.
	const subscribersRef = useRef<Set<(msg: ServerMessage) => void>>(new Set());

	const subscribe = useCallback((handler: (msg: ServerMessage) => void) => {
		subscribersRef.current.add(handler);
		return () => {
			subscribersRef.current.delete(handler);
		};
	}, []);

	const finalizeStreaming = useCallback(() => {
		if (streamingIdRef.current && streamingRef.current) {
			const id = streamingIdRef.current;
			const content = streamingRef.current;
			setEntries((prev) =>
				prev.map((e) => (e.id === id ? { ...e, content } : e)),
			);
		}
		streamingRef.current = "";
		streamingIdRef.current = "";
	}, []);

	const finalizeReasoning = useCallback(() => {
		if (reasoningEntryIdRef.current && reasoningRef.current) {
			const id = reasoningEntryIdRef.current;
			const content = reasoningRef.current;
			setEntries((prev) =>
				prev.map((e) => (e.id === id ? { ...e, content } : e)),
			);
		}
		reasoningRef.current = "";
		reasoningIdRef.current = "";
		reasoningEntryIdRef.current = "";
	}, []);

	// Stable ref for the message handler so the WebSocket effect doesn't re-run
	const handleMessageRef = useRef<(msg: ServerMessage) => void>(() => {});

	handleMessageRef.current = (msg: ServerMessage) => {
		for (const sub of subscribersRef.current) {
			sub(msg);
		}
		switch (msg.type) {
			case "messages": {
				setEntries(messagesToEntries(msg.messages));
				break;
			}

			case "text_delta": {
				finalizeReasoning();
				if (!streamingIdRef.current) {
					const id = nextId();
					streamingIdRef.current = id;
					streamingRef.current = "";
					setEntries((prev) => [
						...prev,
						{ id, type: "assistant", content: "" },
					]);
				}
				streamingRef.current += msg.text;
				const id = streamingIdRef.current;
				const content = streamingRef.current;
				setEntries((prev) =>
					prev.map((e) => (e.id === id ? { ...e, content } : e)),
				);
				break;
			}

			case "reasoning_delta": {
				finalizeStreaming();
				// A new reasoning item id means a new reasoning block — finalize
				// the previous one and start fresh.
				if (
					reasoningEntryIdRef.current &&
					reasoningIdRef.current !== msg.id
				) {
					finalizeReasoning();
				}
				if (!reasoningEntryIdRef.current) {
					const entryId = nextId();
					reasoningEntryIdRef.current = entryId;
					reasoningIdRef.current = msg.id;
					reasoningRef.current = "";
					setEntries((prev) => [
						...prev,
						{
							id: entryId,
							type: "reasoning",
							content: "",
							reasoningId: msg.id,
						},
					]);
				}
				reasoningRef.current += msg.text;
				const entryId = reasoningEntryIdRef.current;
				const content = reasoningRef.current;
				setEntries((prev) =>
					prev.map((e) => (e.id === entryId ? { ...e, content } : e)),
				);
				break;
			}

			case "tool_call": {
				finalizeStreaming();
				finalizeReasoning();
				setEntries((prev) => [
					...prev,
					{
						id: msg.id || nextId(),
						type: "tool",
						content: "",
						toolId: msg.id,
						toolName: msg.name,
						toolArgs: msg.args,
						toolHint: msg.hint,
					},
				]);
				break;
			}

			case "tool_result": {
				setEntries((prev) => {
					const idx = prev.findLastIndex(
						(e) => e.type === "tool" && e.toolId === msg.id,
					);
					if (idx >= 0) {
						const updated = [...prev];
						updated[idx] = { ...updated[idx], toolResult: msg.content };
						return updated;
					}
					return prev;
				});
				break;
			}

			case "phase":
				setPhase(msg.phase);
				break;

			case "prompt":
				setPrompt({ type: "prompt", question: msg.question });
				break;

			case "ask":
				setPrompt({ type: "ask", question: msg.question });
				break;

			case "error":
				finalizeStreaming();
				finalizeReasoning();
				setEntries((prev) => [
					...prev,
					{ id: nextId(), type: "error", content: msg.message },
				]);
				break;

			case "done":
				finalizeStreaming();
				finalizeReasoning();
				break;

			case "usage":
				setUsage({
					inputTokens: msg.input_tokens,
					outputTokens: msg.output_tokens,
				});
				break;
		}
	};

	const send = useCallback((msg: ClientMessage) => {
		if (wsRef.current?.readyState === WebSocket.OPEN) {
			wsRef.current.send(JSON.stringify(msg));
		}
	}, []);

	const sendChat = useCallback(
		(text: string, files?: string[]) => {
			const id = nextId();
			setEntries((prev) => [...prev, { id, type: "user", content: text }]);
			send({ type: "send", text, files });
		},
		[send],
	);

	const cancel = useCallback(() => {
		send({ type: "cancel" });
	}, [send]);

	const respondPrompt = useCallback(
		(approved: boolean) => {
			send({ type: "prompt_response", approved });
			setPrompt(null);
		},
		[send],
	);

	const respondAsk = useCallback(
		(answer: string) => {
			send({ type: "ask_response", answer });
			setPrompt(null);
		},
		[send],
	);

	useEffect(() => {
		let reconnectTimer: ReturnType<typeof setTimeout>;
		let alive = true;

		function connect() {
			if (!alive) return;

			const proto = location.protocol === "https:" ? "wss:" : "ws:";
			const ws = new WebSocket(`${proto}//${location.host}/ws/chat`);

			ws.onopen = () => {
				setConnected(true);
				setPhase("idle");
				// Trigger panels to refetch in case state changed while
				// disconnected — fsnotify events don't get replayed across
				// reconnects, so this is the only safety net.
				for (const sub of subscribersRef.current) {
					sub({ type: "diffs_changed" });
					sub({ type: "checkpoints_changed" });
					sub({ type: "sessions_changed" });
					sub({ type: "files_changed" });
					sub({ type: "diagnostics_changed" });
					sub({ type: "capabilities_changed" });
				}
			};

			ws.onclose = () => {
				setConnected(false);
				wsRef.current = null;
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
	}, []);

	return {
		connected,
		phase,
		entries,
		usage,
		prompt,
		sendChat,
		cancel,
		respondPrompt,
		respondAsk,
		setEntries,
		subscribe,
	};
}
