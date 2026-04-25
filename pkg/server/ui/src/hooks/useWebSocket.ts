import { useCallback, useEffect, useRef, useState } from "react";
import type { ClientMessage, Phase, ServerMessage } from "../types/protocol";

export interface ChatEntry {
	id: string;
	type: "user" | "assistant" | "tool" | "error";
	content: string;
	toolName?: string;
	toolArgs?: string;
	toolHint?: string;
	toolResult?: string;
	toolId?: string;
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
	const idCounterRef = useRef(0);

	const nextId = () => String(++idCounterRef.current);

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

	// Stable ref for the message handler so the WebSocket effect doesn't re-run
	const handleMessageRef = useRef<(msg: ServerMessage) => void>(() => {});

	handleMessageRef.current = (msg: ServerMessage) => {
		for (const sub of subscribersRef.current) {
			sub(msg);
		}
		switch (msg.type) {
			case "messages": {
				const restored: ChatEntry[] = [];
				for (const m of msg.messages) {
					for (const c of m.content) {
						if (c.text) {
							restored.push({
								id: nextId(),
								type: m.role === "user" ? "user" : "assistant",
								content: c.text,
							});
						}
						if (c.tool_result) {
							restored.push({
								id: nextId(),
								type: "tool",
								content: "",
								toolName: c.tool_result.name,
								toolArgs: c.tool_result.args,
								toolResult: c.tool_result.content,
							});
						}
					}
				}
				setEntries(restored);
				break;
			}

			case "text_delta": {
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

			case "tool_call": {
				finalizeStreaming();
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
				setEntries((prev) => [
					...prev,
					{ id: nextId(), type: "error", content: msg.message },
				]);
				break;

			case "done":
				finalizeStreaming();
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
