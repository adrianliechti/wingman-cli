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
	// One-shot expand/collapse signal: bumping `tick` triggers each TurnView's
	// override to snap to `open`. Per-turn clicks afterwards override again.
	const [expandSignal, setExpandSignal] = useState<{
		open: boolean;
		tick: number;
	} | null>(null);
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

	// ── collapse anchor ──────────────────────────────────────────────────────
	//
	// Collapsing or expanding a turn's working area shifts everything below
	// (and sometimes above) the affected node. Because the container has
	// `overflow-anchor: none` to keep the streaming pin authoritative, the
	// browser won't compensate — the visible content jumps. To stop that, the
	// trigger sites snapshot a stable anchor element's screen position before
	// the state change, and a layout effect after the commit re-applies that
	// position by adjusting scrollTop. User and final-answer entries are the
	// stable choices — they survive collapse/expand transitions.
	const pendingAnchorRef = useRef<{ id: string; viewportTop: number } | null>(
		null,
	);

	const captureAnchor = useCallback(() => {
		const c = containerRef.current;
		const content = contentRef.current;
		if (!c || !content) return;
		const turns = buildTurns(entries);
		const stable = new Set<string>();
		for (const t of turns) {
			if (t.user) stable.add(t.user.id);
			if (t.final) stable.add(t.final.id);
		}
		const cRect = c.getBoundingClientRect();
		// Prefer a stable entry that intersects the viewport. If the viewport is
		// currently filled by working entries that are about to disappear, fall
		// back to the nearest stable entry below the viewport so content after
		// the collapse does not jump upward.
		let visible: { id: string; viewportTop: number } | null = null;
		let below: { id: string; viewportTop: number } | null = null;
		let above: { id: string; viewportTop: number } | null = null;
		const els = content.querySelectorAll<HTMLElement>("[data-entry-id]");
		for (const el of els) {
			const id = el.dataset.entryId;
			if (!id || !stable.has(id)) continue;
			const rect = el.getBoundingClientRect();
			const viewportTop = rect.top - cRect.top;
			const viewportBottom = rect.bottom - cRect.top;
			if (viewportBottom >= 0 && viewportTop <= cRect.height) {
				visible = { id, viewportTop };
				continue;
			}
			if (viewportTop > cRect.height) {
				below = { id, viewportTop };
				break;
			}
			above = { id, viewportTop };
		}
		pendingAnchorRef.current = visible ?? below ?? above;
	}, [entries]);

	const applyPendingAnchor = useCallback(() => {
		const c = containerRef.current;
		const content = contentRef.current;
		if (!c || !content) return;
		const a = pendingAnchorRef.current;
		if (!a) return;
		pendingAnchorRef.current = null;
		const el = findEntryElement(content, a.id);
		if (!el) return;
		const cRect = c.getBoundingClientRect();
		const newTop = el.getBoundingClientRect().top - cRect.top;
		const delta = newTop - a.viewportTop;
		if (Math.abs(delta) > 0.5) {
			writeScrollTop(c, c.scrollTop + delta);
		}
	}, [writeScrollTop]);

	const toggleExpandAll = useCallback(() => {
		captureAnchor();
		setExpandSignal((prev) => ({
			open: prev ? !prev.open : true,
			tick: (prev?.tick ?? 0) + 1,
		}));
	}, [captureAnchor]);

	const isActive = phase !== "idle";

	// Phase non-idle -> idle triggers the active turn's auto-collapse. If the
	// user scrolled away from the pinned message during streaming, the working
	// area is partly above the viewport and shrinking it would jump the visible
	// content. Snapshot an anchor here (during render, before React commits the
	// collapsed DOM) so the apply step can preserve it. When the user did not
	// scroll, the pin keeps the user message at the top through the collapse
	// and the natural layout is what we want — skip the capture in that case.
	const prevPhaseRef = useRef(phase);
	/* eslint-disable react-hooks/refs -- This is intentionally a pre-commit
	   DOM snapshot for the phase-driven auto-collapse. Function components do
	   not have a getSnapshotBeforeUpdate hook, and a layout effect would run
	   after the working entries have already collapsed. */
	if (prevPhaseRef.current !== "idle" && phase === "idle") {
		if (userScrolledRef.current) captureAnchor();
	}
	prevPhaseRef.current = phase;
	/* eslint-enable react-hooks/refs */

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
			const userEl = findEntryElement(content, last.id);
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
			const userEl = findEntryElement(content, pin.id);
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
			{entries.length > 0 && (
				<button
					type="button"
					onClick={toggleExpandAll}
					className="absolute top-2 right-3 z-10 text-[11px] text-fg-dim hover:text-fg font-mono cursor-pointer px-1.5 py-0.5 rounded hover:bg-bg-hover"
					title="Expand or collapse all turns"
				>
					{expandSignal?.open ? "collapse all" : "expand all"}
				</button>
			)}
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
						{(() => {
							// Split entries into turns: each turn is a user message followed by
							// "working" entries (reasoning, tools, intermediate assistant text)
							// and ending with the final assistant text (or error). When a turn
							// is finished, the working section collapses to a single line so
							// the chat history shows: user → [Worked] → final answer.
							const turns = buildTurns(entries);
							return turns.map((turn, idx) => {
								const isLastTurn = idx === turns.length - 1;
								const isActive = isLastTurn && phase !== "idle";
								return (
									<TurnView
										key={turn.key}
										turn={turn}
										isActive={isActive}
										phase={phase}
										expandSignal={expandSignal}
										captureAnchor={captureAnchor}
										applyPendingAnchor={applyPendingAnchor}
									/>
								);
							});
						})()}
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
}: {
	entry: ChatEntry;
	isStreaming: boolean;
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

	if (entry.type === "reasoning") {
		return <ReasoningView entry={entry} isStreaming={isStreaming} />;
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

interface Turn {
	key: string;
	user: ChatEntry | null;
	working: ChatEntry[];
	final: ChatEntry | null;
}

// Split flat entries into turns: each turn is one user message + everything
// the agent did before its next final answer. Reasoning, tool calls, and
// intermediate assistant text all go into `working`; the last assistant or
// error entry of the turn becomes `final`. This lets us render finished turns
// as `user → [Worked] → final answer` and only blow open the working section
// while a turn is in flight.
function buildTurns(entries: ChatEntry[]): Turn[] {
	const turns: Turn[] = [];
	let counter = 0;

	for (const e of entries) {
		// Start a new turn on a user message, or on the first entry if history
		// resumes mid-turn (no preceding user).
		if (e.type === "user" || turns.length === 0) {
			turns.push({
				key: e.type === "user" ? e.id : `turn-${counter++}`,
				user: e.type === "user" ? e : null,
				working: [],
				final: null,
			});
			if (e.type === "user") continue;
		}
		const t = turns[turns.length - 1];
		if (e.type === "assistant" || e.type === "error") {
			// New terminal entry — bump the previous final (if any) into working
			// so only the latest assistant text is treated as "the answer".
			if (t.final) t.working.push(t.final);
			t.final = e;
		} else {
			t.working.push(e);
		}
	}

	return turns;
}

function findEntryElement(root: HTMLElement, id: string): HTMLElement | null {
	for (const el of root.querySelectorAll<HTMLElement>("[data-entry-id]")) {
		if (el.dataset.entryId === id) return el;
	}
	return null;
}

function TurnView({
	turn,
	isActive,
	phase,
	expandSignal,
	captureAnchor,
	applyPendingAnchor,
}: {
	turn: Turn;
	isActive: boolean;
	phase: Phase;
	expandSignal: { open: boolean; tick: number } | null;
	captureAnchor: () => void;
	applyPendingAnchor: () => void;
}) {
	// Collapsed when: turn is finished AND has a final answer AND has working
	// entries to hide. While the agent is still working (no final yet, or
	// phase !== idle), the working section stays open so the user can watch.
	// User clicks pin an override.
	const canCollapse =
		!isActive && turn.final !== null && turn.working.length > 0;
	// Override is bound to the signal tick it was set against. A later signal
	// bump (tick > override.tick) supersedes the override; a click made after
	// a signal stamps itself with that signal's tick so the *next* signal will
	// supersede it. This makes "expand all" → "collapse one" → "expand all"
	// behave the way users expect without an effect or store of per-turn state.
	const [override, setOverride] = useState<{
		value: boolean;
		tick: number;
	} | null>(null);
	const signalTick = expandSignal?.tick ?? 0;
	const signalWins = signalTick > (override?.tick ?? 0);
	const effective = signalWins ? expandSignal?.open : override?.value;
	const expanded = effective ?? !canCollapse;
	const setExpanded = (value: boolean) => {
		// Snapshot pre-mutation scroll position so the apply effect below can
		// keep the visible content stable across the per-turn collapse/expand.
		captureAnchor();
		setOverride({ value, tick: signalTick });
	};

	// After the working area's commit lands (auto-collapse, manual click, or
	// expand-all signal), restore any pending anchor. Child effects run before
	// the parent's, so this fires before ChatPanel's spacer-trim sees the new
	// scrollTop. Skip the initial mount — only transitions matter.
	const skipFirstRef = useRef(true);
	useLayoutEffect(() => {
		if (skipFirstRef.current) {
			skipFirstRef.current = false;
			return;
		}
		applyPendingAnchor();
	}, [expanded, applyPendingAnchor]);

	return (
		<>
			{turn.user && <EntryView entry={turn.user} isStreaming={false} />}
			{turn.working.length > 0 &&
				(expanded ? (
					<>
						<WorkingExpanded
							entries={turn.working}
							isActive={isActive}
							phase={phase}
							canCollapse={canCollapse}
							onCollapse={() => setExpanded(false)}
						/>
						{isActive &&
							phase !== "streaming" &&
							turn.working[turn.working.length - 1]?.type !==
								"reasoning" && <PhaseIndicator phase={phase} />}
					</>
				) : (
					<WorkingSummary
						entries={turn.working}
						onExpand={() => setExpanded(true)}
					/>
				))}
			{turn.final && (
				<EntryView
					entry={turn.final}
					isStreaming={
						isActive &&
						phase === "streaming" &&
						turn.final.type === "assistant"
					}
				/>
			)}
			{/* Active turn with no final yet and no working: still show indicator */}
			{isActive &&
				turn.working.length === 0 &&
				!turn.final &&
				phase !== "streaming" && <PhaseIndicator phase={phase} />}
		</>
	);
}

// Renders the full working trail using the same tool-grouping pass as before,
// plus a "collapse" affordance once the turn is finished.
function WorkingExpanded({
	entries,
	isActive,
	phase,
	canCollapse,
	onCollapse,
}: {
	entries: ChatEntry[];
	isActive: boolean;
	phase: Phase;
	canCollapse: boolean;
	onCollapse: () => void;
}) {
	const nodes: React.ReactNode[] = [];
	let i = 0;
	while (i < entries.length) {
		const entry = entries[i];
		if (entry.type === "tool") {
			const start = i;
			while (i < entries.length && entries[i].type === "tool") i++;
			const slice = entries.slice(start, i);
			const isTrailing = isActive && i === entries.length;
			nodes.push(
				<ToolGroupView
					key={slice[0].id}
					entries={slice}
					isTrailing={isTrailing}
					phase={phase}
				/>,
			);
			continue;
		}
		const isLastWorking = i === entries.length - 1;
		const isStreaming =
			isActive &&
			isLastWorking &&
			phase === "thinking" &&
			entry.type === "reasoning";
		nodes.push(
			<EntryView key={entry.id} entry={entry} isStreaming={isStreaming} />,
		);
		i++;
	}

	return (
		<>
			{nodes}
			{canCollapse && (
				<button
					type="button"
					onClick={onCollapse}
					className="mb-4 -mt-2 ml-3 text-[11px] text-fg-dim hover:text-fg font-mono cursor-pointer"
				>
					collapse
				</button>
			)}
		</>
	);
}

function WorkingSummary({
	entries,
	onExpand,
}: {
	entries: ChatEntry[];
	onExpand: () => void;
}) {
	const tools = entries.filter((e) => e.type === "tool").length;
	const thoughts = entries.filter((e) => e.type === "reasoning").length;
	const parts: string[] = [];
	if (thoughts) parts.push(`${thoughts} thought${thoughts === 1 ? "" : "s"}`);
	if (tools) parts.push(`${tools} tool${tools === 1 ? "" : "s"}`);
	const summary = parts.length > 0 ? parts.join(", ") : "Worked";

	return (
		<div className="mb-4 border-l-2 border-purple pl-3">
			<button
				type="button"
				onClick={onExpand}
				className="flex items-center gap-2 py-0.5 cursor-pointer text-[12px]"
			>
				<ChevronRight size={12} className="text-fg-dim shrink-0" />
				<span className="text-fg-dim font-mono text-[11px]">{summary}</span>
			</button>
		</div>
	);
}

function PhaseIndicator({ phase }: { phase: Phase }) {
	// Caller gates on phase !== "streaming", so the only labels reachable here
	// are "thinking" and "tool_running".
	const label = phase === "tool_running" ? "Working" : "Thinking";

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

function ToolGroupView({
	entries,
	isTrailing,
	phase,
}: {
	entries: ChatEntry[];
	isTrailing: boolean;
	phase: Phase;
}) {
	return (
		<div className="mb-4 border-l-2 border-purple pl-3">
			{entries.map((entry, idx) => {
				const isLastInGroup = idx === entries.length - 1;
				const running =
					isTrailing && isLastInGroup && phase !== "idle" && !entry.toolResult;
				return <ToolRow key={entry.id} entry={entry} running={running} />;
			})}
		</div>
	);
}

function ToolRow({
	entry,
	running,
}: {
	entry: ChatEntry;
	running: boolean;
}) {
	const [expanded, setExpanded] = useState(false);
	const hint = entry.toolHint || extractHint(entry.toolArgs, entry.toolName);
	const displayHint = hint ? truncate(hint, 80) : "";

	return (
		<div data-entry-id={entry.id}>
			<div
				className="flex items-center gap-2 py-0.5 cursor-pointer text-[12px] transition-colors"
				onClick={() => setExpanded(!expanded)}
			>
				<span className="text-fg-dim shrink-0 flex items-center">
					{running ? (
						<LoaderCircle size={11} className="animate-spin" />
					) : expanded ? (
						<ChevronDown size={12} />
					) : (
						<ChevronRight size={12} />
					)}
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
				<div className="mt-1 mb-1 px-3 py-2 text-[11px] whitespace-pre-wrap break-all text-fg-dim bg-bg-surface rounded-md font-mono leading-relaxed">
					{truncate(entry.toolResult || "(no output)", 2000)}
				</div>
			)}
		</div>
	);
}

function ReasoningView({
	entry,
	isStreaming,
}: {
	entry: ChatEntry;
	isStreaming: boolean;
}) {
	const summary = entry.content || "";
	if (!summary) return null;

	return (
		<div data-entry-id={entry.id} className="mb-4 border-l-2 border-purple pl-3">
			<div className="text-[11px] whitespace-pre-wrap break-words text-fg-dim font-mono leading-relaxed italic">
				{summary}
				{isStreaming && (
					<span className="inline-block w-[5px] h-[10px] bg-fg-dim align-text-bottom ml-0.5 animate-[blink_1s_step-end_infinite]" />
				)}
			</div>
		</div>
	);
}

// Mirror of pkg/tui/format.go: ExtractToolHint. The server pre-computes Hint
// using the Go helper, so this only runs as a fallback (e.g. when args are
// available but Hint isn't). Keep the rules identical.
const FS_TOOLS = new Set(["read", "write", "edit", "ls", "find", "grep"]);
const WORKING_DIR_TOOLS = new Set(["ls", "find", "grep"]);

function extractHint(argsJSON?: string, toolName?: string): string {
	const wdFallback = toolName && WORKING_DIR_TOOLS.has(toolName) ? "/" : "";
	if (!argsJSON) return wdFallback;
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
			const v = args[key];
			if (typeof v !== "string" || !v) continue;
			if ((key === "path" || key === "file") && toolName && FS_TOOLS.has(toolName)) {
				return normalizeWorkspacePath(v);
			}
			return v;
		}
	} catch {
		/* ignore */
	}
	return wdFallback;
}

function normalizeWorkspacePath(p: string): string {
	if (p === "" || p === "." || p === "./") return "/";
	if (p.startsWith("/") || p.startsWith("~")) return p;
	return "/" + p;
}

function truncate(text: string, max: number): string {
	return text.length <= max ? text : `${text.substring(0, max)}...`;
}
