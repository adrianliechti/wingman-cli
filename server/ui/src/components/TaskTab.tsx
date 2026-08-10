import { Loader2 } from "lucide-react";
import {
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
} from "react";
import { messagesToEntries } from "../hooks/useWebSocket";
import type { ServerMessage, TaskDetail } from "../types/protocol";
import { MarkdownContent } from "./MarkdownContent";
import { TurnView } from "./chat/TurnView";
import { buildTurns } from "./chat/turns";

interface Props {
	sessionId: string;
	taskId: string;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

const noAnchor = () => {};

export function TaskTab({ sessionId, taskId, subscribe }: Props) {
	const [detail, setDetail] = useState<TaskDetail | null>(null);
	const [error, setError] = useState<string | null>(null);
	const loadedRef = useRef(false);

	const load = useCallback(async () => {
		if (!sessionId) return;
		try {
			const res = await fetch(`/api/sessions/${sessionId}/tasks/${taskId}`);
			if (!res.ok) {
				if (!loadedRef.current) setError("Agent no longer available.");
				return;
			}
			loadedRef.current = true;
			setDetail(await res.json());
			setError(null);
		} catch {
			if (!loadedRef.current) setError("Failed to load agent.");
		}
	}, [sessionId, taskId]);

	useEffect(() => {
		load();
	}, [load]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "tasks_changed") load();
		});
	}, [subscribe, load]);

	const running = detail?.status === "running";

	useEffect(() => {
		if (!running) return;
		const timer = setInterval(load, 3000);
		return () => clearInterval(timer);
	}, [running, load]);

	const scrollRef = useRef<HTMLDivElement>(null);
	const stickRef = useRef(true);

	const handleScroll = useCallback(() => {
		const el = scrollRef.current;
		if (!el) return;
		stickRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
	}, []);

	useLayoutEffect(() => {
		if (!running || !stickRef.current) return;
		const el = scrollRef.current;
		if (el) el.scrollTop = el.scrollHeight;
	}, [running, detail]);

	const { turns, resultShown } = useMemo(() => {
		if (!detail) return { turns: [], resultShown: false };
		const entries = messagesToEntries(detail.transcript);
		const lastText = [...entries].reverse().find((e) => e.type === "assistant");
		return {
			turns: buildTurns(entries),
			resultShown:
				!!detail.result && lastText?.content.trim() === detail.result.trim(),
		};
	}, [detail]);

	if (!detail) {
		return (
			<div className="h-full flex items-center justify-center bg-bg">
				{error ? (
					<div className="max-w-sm px-4 text-center text-[12px] text-fg-dim">
						{error}
					</div>
				) : (
					<Loader2 size={16} className="text-fg-dim animate-spin" />
				)}
			</div>
		);
	}

	return (
		<div
			ref={scrollRef}
			onScroll={handleScroll}
			className="h-full overflow-y-auto [overflow-anchor:none] bg-bg"
		>
			<div className="px-4 py-4">
				{turns.map((turn) => (
					<TurnView
						key={turn.key}
						turn={turn}
						isActive={false}
						phase="idle"
						applyPendingAnchor={noAnchor}
					/>
				))}
				{detail.status === "running" && (
					<div className="flex items-center gap-2 pl-3 text-[12px] text-fg-dim font-mono italic">
						<Loader2 size={12} className="animate-spin shrink-0" />
						<span className="truncate">{detail.activity || "working…"}</span>
					</div>
				)}
				{detail.status !== "running" && detail.result && !resultShown && (
					<div className="border-l-2 border-purple pl-3 text-[12px] leading-[1.7] font-mono break-words">
						<MarkdownContent text={detail.result} />
					</div>
				)}
			</div>
		</div>
	);
}
