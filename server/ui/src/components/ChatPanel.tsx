import {
	ArrowUp,
	ChevronDown,
	ChevronRight,
	LoaderCircle,
	Plus,
	Square,
	X,
} from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { ChatEntry } from "../hooks/useWebSocket";
import type { Phase } from "../types/protocol";
import { FilePicker } from "./FilePicker";
import { MarkdownContent } from "./MarkdownContent";
import { ModelPicker } from "./ModelPicker";
import { ModePicker } from "./ModePicker";
import { SkillPicker } from "./SkillPicker";

interface Props {
	entries: ChatEntry[];
	phase: Phase;
	onSend: (text: string, files?: string[]) => void;
	onCancel: () => void;
}

// Visual gap left above a pinned user message — matches the contentRef's
// py-4 padding so the first message and subsequent submissions look the same.
const PIN_TOP_GAP = 16;

export function ChatPanel({ entries, phase, onSend, onCancel }: Props) {
	const [input, setInput] = useState("");
	const [files, setFiles] = useState<string[]>([]);
	const [showPicker, setShowPicker] = useState(false);
	const containerRef = useRef<HTMLDivElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	const spacerRef = useRef<HTMLDivElement>(null);
	const textareaRef = useRef<HTMLTextAreaElement>(null);

	// ── scroll handling ──────────────────────────────────────────────────────
	//
	// On submit: pin the new user message to the viewport top and reserve a
	// full viewport of empty room below it via the spacer. While the response
	// streams, re-assert scrollTop = pin.top on every render so layout shifts
	// (PhaseIndicator toggling, reasoning growing, etc.) can't drift it.
	// User scroll yields the hold. On phase=idle: release the pin and trim
	// the spacer to its minimum.
	const submitPendingRef = useRef(false);
	const pinRef = useRef<{ id: string; top: number } | null>(null);
	const userScrolledRef = useRef(false);
	// Scroll events fired before this timestamp are treated as our own writes.
	const programmaticUntilRef = useRef(0);
	// True after the first non-empty paint has been handled (session restore).
	const restoredRef = useRef(false);

	const writeScrollTop = useCallback((el: HTMLElement, top: number) => {
		programmaticUntilRef.current = performance.now() + 100;
		el.scrollTop = top;
	}, []);

	const isActive = phase !== "idle";

	// Show the skill picker while the user is still typing the slash command
	// (no whitespace yet — once they add a space we treat the rest as args).
	const skillMatch = input.match(/^\/(\S*)$/);
	const showSkills = !!skillMatch && !isActive;
	const skillQuery = skillMatch ? skillMatch[1] : "";

	// One-shot: jump to bottom on the first paint with restored history (not
	// on a fresh user submission — that's handled by the pin logic below).
	useLayoutEffect(() => {
		if (restoredRef.current || entries.length === 0) return;
		restoredRef.current = true;
		if (submitPendingRef.current) return;
		const el = containerRef.current;
		if (el) writeScrollTop(el, el.scrollHeight);
	}, [entries, writeScrollTop]);

	// Pin / hold / release — single state machine driven by entries + phase.
	useLayoutEffect(() => {
		const container = containerRef.current;
		const content = contentRef.current;
		const spacer = spacerRef.current;
		if (!container || !content || !spacer) return;

		// (1) Pin: a fresh submission is in flight and the user message just
		// landed in the DOM.
		if (submitPendingRef.current) {
			const last = entries[entries.length - 1];
			if (last?.type !== "user") return;
			const userEl = content.querySelector(
				`[data-entry-id="${last.id}"]`,
			) as HTMLElement | null;
			if (!userEl) return;

			submitPendingRef.current = false;
			userScrolledRef.current = false;

			// Reserve viewport-height of room so the message can reach the top
			// regardless of what's below.
			spacer.style.height = `${container.clientHeight}px`;
			const cRect = container.getBoundingClientRect();
			const uRect = userEl.getBoundingClientRect();
			// Leave a small gap above the pinned message so it doesn't sit
			// flush with the top edge — matches the natural py-4 padding the
			// first message has.
			const top = Math.max(
				0,
				uRect.top - cRect.top + container.scrollTop - PIN_TOP_GAP,
			);
			pinRef.current = { id: last.id, top };
			writeScrollTop(container, top);
			return;
		}

		const pin = pinRef.current;
		if (!pin) return;

		// (3) Release: response settled — trim the spacer to its minimum.
		// Never shrink below what's needed to keep the current scrollTop
		// reachable, otherwise a user who scrolled down past the pin during
		// streaming gets yanked up when the spacer collapses.
		if (phase === "idle") {
			pinRef.current = null;
			const belowUser =
				container.scrollHeight - pin.top - spacer.offsetHeight;
			const minForPin = Math.max(0, container.clientHeight - belowUser);
			const minForUser = Math.max(
				0,
				container.scrollTop +
					container.clientHeight -
					(container.scrollHeight - spacer.offsetHeight),
			);
			spacer.style.height = `${Math.max(minForPin, minForUser)}px`;
			return;
		}

		// (2) Hold: re-assert scrollTop while streaming, unless the user took
		// over. Browser scroll-anchoring and other layout-shift compensations
		// can otherwise drift the pinned message off the top.
		if (userScrolledRef.current) return;
		if (Math.abs(container.scrollTop - pin.top) > 2) {
			writeScrollTop(container, pin.top);
		}
	}, [entries, phase, writeScrollTop]);

	// Window resize during streaming: content above the pinned message may
	// rewrap at the new width, shifting its scroll-coordinate top. Recompute
	// and re-snap so the message stays anchored.
	useEffect(() => {
		if (phase === "idle") return;
		const container = containerRef.current;
		const content = contentRef.current;
		const spacer = spacerRef.current;
		if (!container || !content || !spacer) return;

		const onResize = () => {
			const pin = pinRef.current;
			if (!pin || userScrolledRef.current) return;
			const userEl = content.querySelector(
				`[data-entry-id="${pin.id}"]`,
			) as HTMLElement | null;
			if (!userEl) return;

			const cRect = container.getBoundingClientRect();
			const uRect = userEl.getBoundingClientRect();
			const top = Math.max(
				0,
				uRect.top - cRect.top + container.scrollTop - PIN_TOP_GAP,
			);
			pin.top = top;
			spacer.style.height = `${container.clientHeight}px`;
			writeScrollTop(container, top);
		};

		window.addEventListener("resize", onResize);
		return () => window.removeEventListener("resize", onResize);
	}, [phase, writeScrollTop]);

	// Detect intentional user scroll. programmaticUntilRef gates out the
	// scroll events fired by our own writes.
	useEffect(() => {
		const container = containerRef.current;
		if (!container) return;
		const onScroll = () => {
			if (performance.now() < programmaticUntilRef.current) return;
			userScrolledRef.current = true;
		};
		container.addEventListener("scroll", onScroll, { passive: true });
		return () => container.removeEventListener("scroll", onScroll);
	}, []);

	const handleSubmit = useCallback(() => {
		const text = input.trim();
		if (!text || isActive) return;
		submitPendingRef.current = true;
		onSend(text, files.length > 0 ? files : undefined);
		setInput("");
		setFiles([]);
	}, [input, isActive, onSend, files]);

	const handleKeyDown = useCallback(
		(e: React.KeyboardEvent) => {
			// Let SkillPicker handle Enter / Tab / arrows / Escape while it's open.
			if (showSkills && (e.key === "Enter" || e.key === "Tab" || e.key === "ArrowDown" || e.key === "ArrowUp" || e.key === "Escape")) {
				return;
			}
			if (e.key === "Enter" && !e.shiftKey) {
				e.preventDefault();
				handleSubmit();
			}
			if (e.key === "Escape" && isActive) {
				onCancel();
			}
		},
		[handleSubmit, isActive, onCancel, showSkills],
	);

	const addFile = useCallback((path: string) => {
		setFiles((prev) => (prev.includes(path) ? prev : [...prev, path]));
		setShowPicker(false);
	}, []);

	const removeFile = useCallback((path: string) => {
		setFiles((prev) => prev.filter((p) => p !== path));
	}, []);

	const selectSkill = useCallback(
		(s: { name: string; arguments?: string[] }) => {
			const hasArgs = !!s.arguments && s.arguments.length > 0;
			if (hasArgs) {
				// Pre-fill the slash command and let the user type arguments.
				setInput(`/${s.name} `);
				textareaRef.current?.focus();
			} else if (!isActive) {
				// No args — fire the skill immediately.
				submitPendingRef.current = true;
				onSend(`/${s.name}`, files.length > 0 ? files : undefined);
				setInput("");
				setFiles([]);
			}
		},
		[isActive, onSend, files],
	);

	return (
		<div className="h-full relative overflow-hidden bg-bg">
			<div
				className="h-full overflow-y-auto pb-24 [overflow-anchor:none]"
				ref={containerRef}
			>
				{entries.length === 0 && phase === "idle" ? (
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
					<div className="px-4 py-4" ref={contentRef}>
						{entries.map((entry, idx) => {
							const isLast = idx === entries.length - 1;
							const isStreaming =
								isLast &&
								((phase === "streaming" &&
									entry.type === "assistant") ||
									(phase === "thinking" &&
										entry.type === "reasoning"));
							return (
								<EntryView
									key={entry.id}
									entry={entry}
									isStreaming={isStreaming}
									hasFollowing={!isLast}
								/>
							);
						})}
						{phase !== "idle" &&
							phase !== "streaming" &&
							entries[entries.length - 1]?.type !== "reasoning" && (
								<PhaseIndicator phase={phase} />
							)}
					</div>
				)}
				{/* Spacer lives outside contentRef so writing its height doesn't
				    resize the observed element and avoids the ResizeObserver loop
				    warning. */}
				<div ref={spacerRef} aria-hidden style={{ height: 0 }} />
			</div>

			{/* Floating input */}
			<div className="absolute bottom-0 left-0 right-0">
				<div className="h-6 bg-gradient-to-t from-bg to-transparent pointer-events-none" />
				<div className="bg-bg px-4 pb-3">
					<div className="relative rounded-lg border border-border-subtle bg-bg-surface/60 hover:border-border focus-within:border-border transition-colors">
						{showSkills && (
							<SkillPicker
								query={skillQuery}
								onSelect={selectSkill}
								onClose={() => {
									/* picker closes naturally when input no longer matches */
								}}
							/>
						)}
						{files.length > 0 && (
							<div className="flex flex-wrap gap-1 px-2.5 pt-2">
								{files.map((p) => {
									const name = p.split("/").pop() || p;
									return (
										<span
											key={p}
											className="group flex items-center gap-1 px-1.5 py-0.5 rounded bg-bg-active text-[11px] text-fg-muted font-mono"
											title={p}
										>
											<span className="truncate max-w-[180px]">{name}</span>
											<button
												type="button"
												className="text-fg-dim hover:text-fg cursor-pointer"
												onClick={() => removeFile(p)}
												aria-label="Remove file"
											>
												<X size={10} />
											</button>
										</span>
									);
								})}
							</div>
						)}

						<div className="px-3 pt-2">
							<textarea
								ref={textareaRef}
								className="w-full bg-transparent text-fg text-[12px] font-mono resize-none outline-none leading-[1.7] placeholder:text-fg-dim"
								style={{ fieldSizing: "content" } as React.CSSProperties}
								value={input}
								onChange={(e) => setInput(e.target.value)}
								onKeyDown={handleKeyDown}
								placeholder="Message Wingman…"
								rows={1}
							/>
						</div>

						<div className="flex items-center justify-between px-1.5 pb-1.5 pt-1 gap-1">
							<div className="flex items-center gap-0 min-w-0">
								<div className="relative flex items-center">
									<button
										type="button"
										className="w-7 h-7 flex items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
										onClick={() => setShowPicker((s) => !s)}
										title="Add file context"
									>
										<Plus size={14} />
									</button>
									{showPicker && (
										<FilePicker
											onSelect={addFile}
											onClose={() => setShowPicker(false)}
										/>
									)}
								</div>
								<ModePicker />
								<ModelPicker />
							</div>

							<button
								type="button"
								className={`group w-7 h-7 flex items-center justify-center rounded cursor-pointer transition-colors ${
									isActive || input.trim()
										? "text-fg-muted hover:text-fg hover:bg-bg-hover"
										: "text-fg-dim opacity-40 cursor-not-allowed"
								}`}
								onClick={isActive ? onCancel : handleSubmit}
								disabled={!isActive && !input.trim()}
								title={isActive ? "Stop (Esc)" : "Send (Enter)"}
							>
								{isActive ? (
									<>
										<LoaderCircle
											size={14}
											className="animate-spin group-hover:hidden"
										/>
										<Square
											size={10}
											fill="currentColor"
											className="hidden group-hover:block"
										/>
									</>
								) : (
									<ArrowUp size={14} />
								)}
							</button>
						</div>
					</div>
				</div>
			</div>
		</div>
	);
}

function EntryView({
	entry,
	isStreaming,
	hasFollowing,
}: {
	entry: ChatEntry;
	isStreaming: boolean;
	hasFollowing: boolean;
}) {
	if (entry.type === "error") {
		return (
			<div data-entry-id={entry.id} className="mb-4 border-l-2 border-danger pl-3">
				<div className="text-[13px] leading-relaxed text-danger break-words">
					{entry.content}
				</div>
			</div>
		);
	}

	if (entry.type === "tool") {
		return <ToolCallView entry={entry} />;
	}

	if (entry.type === "reasoning") {
		return (
			<ReasoningView
				entry={entry}
				isStreaming={isStreaming}
				hasFollowing={hasFollowing}
			/>
		);
	}

	const isUser = entry.type === "user";

	return (
		<div
			data-entry-id={entry.id}
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

function PhaseIndicator({ phase }: { phase: Phase }) {
	let label = "Thinking";
	if (phase === "tool_running") label = "Running tool";
	else if (phase === "streaming") label = "Streaming";

	return (
		<div className="mb-4 pl-3">
			<div className="flex items-baseline gap-1.5 text-[12px] text-fg-dim font-mono italic">
				<span>{label}</span>
				<span className="inline-flex gap-[3px]">
					<span
						className="w-[3px] h-[3px] rounded-full bg-fg-dim animate-pulse"
						style={{ animationDelay: "0ms", animationDuration: "1.2s" }}
					/>
					<span
						className="w-[3px] h-[3px] rounded-full bg-fg-dim animate-pulse"
						style={{ animationDelay: "200ms", animationDuration: "1.2s" }}
					/>
					<span
						className="w-[3px] h-[3px] rounded-full bg-fg-dim animate-pulse"
						style={{ animationDelay: "400ms", animationDuration: "1.2s" }}
					/>
				</span>
			</div>
		</div>
	);
}

function ToolCallView({ entry }: { entry: ChatEntry }) {
	const [expanded, setExpanded] = useState(false);
	const hint = entry.toolHint || extractHint(entry.toolArgs);
	const displayHint = hint ? truncate(hint, 80) : "";

	return (
		<div data-entry-id={entry.id} className="mb-4 border-l-2 border-purple pl-3">
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

function ReasoningView({
	entry,
	isStreaming,
	hasFollowing,
}: {
	entry: ChatEntry;
	isStreaming: boolean;
	hasFollowing: boolean;
}) {
	// Auto-expand while this is the active/last entry; auto-collapse once a
	// later entry (tool call, assistant text, …) supersedes it. A user click
	// pins their preference and overrides the auto behavior from then on.
	const [override, setOverride] = useState<boolean | null>(null);
	const expanded = override ?? !hasFollowing;
	const summary = entry.content || "";

	return (
		<div data-entry-id={entry.id} className="mb-4 border-l-2 border-purple pl-3">
			<button
				type="button"
				className="flex items-center gap-2 py-0.5 cursor-pointer text-[12px] transition-colors"
				onClick={() => setOverride(!expanded)}
			>
				<span className="text-fg-dim shrink-0 flex items-center">
					{expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
				</span>
				<span className="text-purple font-mono text-[11px] shrink-0">
					Thinking
				</span>
			</button>
			{expanded && summary && (
				<div className="mt-0.5 text-[11px] whitespace-pre-wrap break-words text-fg-dim font-mono leading-relaxed italic">
					{summary}
					{isStreaming && (
						<span className="inline-block w-[5px] h-[10px] bg-fg-dim align-text-bottom ml-0.5 animate-[blink_1s_step-end_infinite]" />
					)}
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
