import { ChevronDown, ChevronRight, Square } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { ChatEntry } from "../hooks/useWebSocket";
import type { Phase } from "../types/protocol";
import { MarkdownContent } from "./MarkdownContent";

interface Props {
	entries: ChatEntry[];
	phase: Phase;
	onSend: (text: string) => void;
	onCancel: () => void;
}

export function ChatPanel({ entries, phase, onSend, onCancel }: Props) {
	const [input, setInput] = useState("");
	const messagesRef = useRef<HTMLDivElement>(null);

	const isActive = phase !== "idle";

	useEffect(() => {
		const el = messagesRef.current;
		if (el) el.scrollTop = el.scrollHeight;
	}, []);

	const handleSubmit = useCallback(() => {
		const text = input.trim();
		if (!text || isActive) return;
		onSend(text);
		setInput("");
	}, [input, isActive, onSend]);

	const handleKeyDown = useCallback(
		(e: React.KeyboardEvent) => {
			if (e.key === "Enter" && !e.shiftKey) {
				e.preventDefault();
				handleSubmit();
			}
			if (e.key === "Escape" && isActive) {
				onCancel();
			}
		},
		[handleSubmit, isActive, onCancel],
	);

	return (
		<div className="h-full relative overflow-hidden bg-bg">
			<div className="h-full overflow-y-auto pb-24" ref={messagesRef}>
				{entries.length === 0 ? (
					<div className="h-full flex items-center justify-center">
						<div className="text-center max-w-sm">
							<div className="text-[28px] font-semibold text-fg mb-2">
								Wingman
							</div>
							<div className="text-[13px] text-fg-dim leading-relaxed">
								Ask me to write code, fix bugs, explore files, or run commands.
							</div>
						</div>
					</div>
				) : (
					<div className="px-4 py-4">
						{entries.map((entry) => (
							<EntryView
								key={entry.id}
								entry={entry}
								isStreaming={
									phase === "streaming" &&
									entry === entries[entries.length - 1] &&
									entry.type === "assistant"
								}
							/>
						))}
					</div>
				)}
			</div>

			{/* Floating input */}
			<div className="absolute bottom-0 left-0 right-0">
				<div className="h-6 bg-gradient-to-t from-bg to-transparent pointer-events-none" />
				<div className="bg-bg px-4 pb-3">
					<div className="flex items-start gap-0 font-mono text-[12px]">
						<span className="text-success leading-[1.7] select-none shrink-0 text-[14px]">
							{isActive ? "" : ">"}&nbsp;
						</span>
						{isActive ? (
							<div className="flex items-center gap-2 text-fg-dim">
								<span className="animate-[pulse_1s_infinite]">working...</span>
								<button
									type="button"
									className="text-fg-dim hover:text-fg cursor-pointer transition-colors"
									onClick={onCancel}
									title="Stop (Esc)"
								>
									<Square size={10} fill="currentColor" />
								</button>
							</div>
						) : (
							<textarea
								className="flex-1 bg-transparent text-fg resize-none outline-none leading-[1.7] placeholder:text-fg-dim"
								style={{ fieldSizing: "content" } as React.CSSProperties}
								value={input}
								onChange={(e) => setInput(e.target.value)}
								onKeyDown={handleKeyDown}
								placeholder=""
								rows={1}
							/>
						)}
					</div>
				</div>
			</div>
		</div>
	);
}

function EntryView({
	entry,
	isStreaming,
}: {
	entry: ChatEntry;
	isStreaming: boolean;
}) {
	if (entry.type === "error") {
		return (
			<div className="mb-4 border-l-2 border-danger pl-3">
				<div className="text-[13px] leading-relaxed text-danger break-words">
					{entry.content}
				</div>
			</div>
		);
	}

	if (entry.type === "tool") {
		return <ToolCallView entry={entry} />;
	}

	const isUser = entry.type === "user";

	return (
		<div
			className={`mb-4 border-l-2 ${isUser ? "border-success" : "border-purple"} pl-3`}
		>
			<div className="text-[12px] leading-[1.7] break-words min-w-0 font-mono">
				{isUser ? (
					<span className="whitespace-pre-wrap text-fg">{entry.content}</span>
				) : (
					<>
						<MarkdownContent text={entry.content} />
						{isStreaming && (
							<span className="inline-block w-[6px] h-[14px] bg-fg-dim align-text-bottom ml-0.5 animate-[blink_1s_step-end_infinite]" />
						)}
					</>
				)}
			</div>
		</div>
	);
}

function ToolCallView({ entry }: { entry: ChatEntry }) {
	const [expanded, setExpanded] = useState(false);
	const hint = entry.toolHint || extractHint(entry.toolArgs);
	const displayHint = hint ? truncate(hint, 80) : "";

	return (
		<div className="mb-4 border-l-2 border-purple pl-3">
			<div
				className="flex items-center gap-2 py-0.5 cursor-pointer text-[12px] transition-colors"
				onClick={() => setExpanded(!expanded)}
			>
				<span className="text-fg-dim shrink-0 flex items-center">
					{expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
				</span>
				<span className="text-purple font-mono text-[11px] shrink-0">
					{entry.toolName}
				</span>
				{displayHint && (
					<span className="text-fg-dim font-mono text-[11px] overflow-hidden text-ellipsis whitespace-nowrap flex-1">
						{displayHint}
					</span>
				)}
			</div>
			{expanded && (
				<div className="mt-1 px-3 py-2 text-[11px] whitespace-pre-wrap break-all text-fg-dim bg-bg-surface rounded-md font-mono leading-relaxed">
					{truncate(entry.toolResult || "(no output)", 2000)}
				</div>
			)}
		</div>
	);
}

function extractHint(argsJSON?: string): string {
	if (!argsJSON) return "";
	try {
		const args = JSON.parse(argsJSON);
		for (const key of [
			"description",
			"query",
			"pattern",
			"command",
			"prompt",
			"path",
			"file",
			"url",
			"name",
		]) {
			if (typeof args[key] === "string" && args[key]) return args[key];
		}
	} catch {
		/* ignore */
	}
	return "";
}

function truncate(text: string, max: number): string {
	return text.length <= max ? text : `${text.substring(0, max)}...`;
}
