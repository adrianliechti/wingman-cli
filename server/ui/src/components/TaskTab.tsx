import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";
import { useCallback, useLayoutEffect, useMemo, useRef } from "react";
import { queryKeys } from "../api/query";
import { getTask } from "../api/tasks";
import { MarkdownContent } from "./MarkdownContent";
import { TurnView } from "./chat/TurnView";
import { buildTurns } from "./chat/turns";

interface Props {
	sessionId: string;
	taskId: string;
}

const noAnchor = () => {};

export function TaskTab({ sessionId, taskId }: Props) {
	const query = useQuery({
		queryKey: queryKeys.tasks.detail(sessionId, taskId),
		enabled: !!sessionId,
		queryFn: ({ signal }) => getTask(sessionId, taskId, signal),
		refetchInterval: (current) =>
			current.state.data?.status === "running" ? 3000 : false,
	});
	const detail = query.data ?? null;
	const error = query.error
		? detail
			? null
			: "Agent no longer available."
		: null;
	const running = detail?.status === "running";

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
		const entries = detail.transcript;
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
