import {
	ArrowUp,
	Loader2,
	LoaderCircle,
	ListPlus,
	Paperclip,
	Plus,
	Square,
	X,
} from "lucide-react";
import {
	useCallback,
	useEffect,
	useLayoutEffect,
	useMemo,
	useRef,
	useState,
} from "react";
import { useColorScheme } from "../hooks/useColorScheme";
import { type Skill, useSkills } from "../hooks/useSkills";
import type {
	ChatEntry,
	PendingPrompt,
	PendingTurnInput,
	PromptReply,
} from "../hooks/useWebSocket";
import type { Phase, TurnInputIntent, TurnInputState } from "../types/protocol";
import { ToolProgressContext } from "./chat/progress";
import { type PendingImage, processImage } from "./chat/images";
import { PromptBar } from "./chat/PromptBar";
import { ScrollSnapshot } from "./chat/ScrollSnapshot";
import { TurnView } from "./chat/TurnView";
import { buildTurns, findEntryElement, type Turn } from "./chat/turns";
import { FilePicker } from "./FilePicker";
import { ModelPicker } from "./ModelPicker";
import { ModePicker, type ModeOption } from "./ModePicker";
import { SkillPicker } from "./SkillPicker";
import { TurnQueue } from "./TurnQueue";

interface Props {
	sessionId?: string;
	placeholder?: string;
	entries: ChatEntry[];
	phase: Phase;
	modes: ModeOption[];
	mode: string;
	onSelectMode: (next: string) => void;
	onSend: (
		text: string,
		files?: string[],
		images?: string[],
		intent?: TurnInputIntent,
	) => boolean | Promise<boolean>;
	onCancel: (clearQueue?: boolean) => void;
	pendingInputs?: PendingTurnInput[];
	queuePaused?: boolean;
	canSteer?: boolean;
	onRemoveQueued?: (id: string, state: TurnInputState) => void;
	onUpdateQueued?: (
		id: string,
		text: string,
		files?: string[],
		images?: string[],
	) => boolean | Promise<boolean>;
	onResumeQueue?: () => void;
	onClearQueue?: () => void;
	loading?: boolean;
	loadError?: string | null;
	error?: string | null;
	onDismissError?: () => void;
	prompts?: PendingPrompt[];
	onPromptReply?: (id: string, reply: PromptReply) => void;
	onOpenFile?: (path: string, line?: number) => void;
	seed?: {
		text: string;
		files?: string[];
		append?: boolean;
		nonce: number;
	} | null;
	onSeedConsumed?: (nonce: number) => void;
	toolProgress?: Record<string, string>;
}

const PIN_TOP_GAP = 16;

// slashTokenAt returns the /command token the caret sits in: the index of its
// leading slash and the typed query behind it. A token starts at the beginning
// of the text or after whitespace and contains no whitespace, so paths and
// URLs never form one.
function slashTokenAt(
	text: string,
	caret: number,
): { start: number; query: string } | null {
	const pos = Math.min(caret, text.length);
	for (let i = pos; i > 0; i--) {
		const ch = text[i - 1];
		if (ch === " " || ch === "\t" || ch === "\n") return null;
		if (ch !== "/") continue;
		if (i >= 2) {
			const prev = text[i - 2];
			if (prev !== " " && prev !== "\t" && prev !== "\n") return null;
		}
		return { start: i - 1, query: text.slice(i, pos) };
	}
	return null;
}

function wordEndAt(text: string, caret: number): number {
	let end = Math.min(caret, text.length);
	while (end < text.length) {
		const code = text.charCodeAt(end);
		if (code === 9 || code === 10 || code === 32) break;
		end++;
	}
	return end;
}

export function ChatPanel({
	sessionId,
	placeholder = "Message Wingman…",
	entries,
	phase,
	modes,
	mode,
	onSelectMode,
	onSend,
	onCancel,
	pendingInputs = [],
	queuePaused = false,
	canSteer = false,
	onRemoveQueued,
	onUpdateQueued,
	onResumeQueue,
	onClearQueue,
	loading,
	loadError,
	error,
	onDismissError,
	prompts = [],
	onPromptReply,
	onOpenFile,
	seed,
	onSeedConsumed,
	toolProgress,
}: Props) {
	const scheme = useColorScheme();
	const [input, setInput] = useState("");
	const [caret, setCaret] = useState(0);
	const [dismissedToken, setDismissedToken] = useState<string | null>(null);
	const [files, setFiles] = useState<string[]>([]);
	const [images, setImages] = useState<PendingImage[]>([]);
	const [showPicker, setShowPicker] = useState(false);
	const [submitting, setSubmitting] = useState(false);
	const [sendError, setSendError] = useState<string | null>(null);
	const inputError = sendError ?? error;
	const [editingQueueId, setEditingQueueId] = useState<string | null>(null);
	const queueSettling =
		pendingInputs.length > 0 &&
		pendingInputs.every((item) => item.state === "sending");
	const [revealSettling, setRevealSettling] = useState(false);
	if (!queueSettling && revealSettling) setRevealSettling(false);
	useEffect(() => {
		if (!queueSettling) return;
		const timer = setTimeout(() => setRevealSettling(true), 400);
		return () => clearTimeout(timer);
	}, [queueSettling]);
	const showQueue =
		pendingInputs.length > 0 && (!queueSettling || revealSettling);
	const historyPadding =
		entries.length === 0 ? "" : showQueue ? "pb-56" : "pb-24";
	const containerRef = useRef<HTMLDivElement>(null);
	const contentRef = useRef<HTMLDivElement>(null);
	const spacerRef = useRef<HTMLDivElement>(null);
	const textareaRef = useRef<HTMLTextAreaElement>(null);
	const imageInputRef = useRef<HTMLInputElement>(null);
	const [composer, setComposer] = useState<HTMLDivElement | null>(null);
	const [filePickerButton, setFilePickerButton] =
		useState<HTMLButtonElement | null>(null);
	const turns = useMemo(() => buildTurns(entries), [entries]);

	const submitPendingRef = useRef(false);
	const historyIdxRef = useRef<number | null>(null);
	const historyDraftRef = useRef("");
	const pinRef = useRef<{ id: string; top: number } | null>(null);
	const userScrolledRef = useRef(false);
	const programmaticUntilRef = useRef(0);
	const restoredRef = useRef(false);

	const writeScrollTop = useCallback((el: HTMLElement, top: number) => {
		programmaticUntilRef.current = performance.now() + 100;
		el.scrollTop = top;
	}, []);

	const pendingAnchorRef = useRef<{ id: string; viewportTop: number } | null>(
		null,
	);

	const captureAnchorForTurns = useCallback((sourceTurns: Turn[]) => {
		const c = containerRef.current;
		const content = contentRef.current;
		if (!c || !content) return;
		const stable = new Set<string>();
		for (const t of sourceTurns) {
			if (t.user) stable.add(t.user.id);
			if (t.final) stable.add(t.final.id);
		}
		const cRect = c.getBoundingClientRect();
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
	}, []);

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

	const isActive = phase !== "idle";

	useEffect(() => {
		if (!isActive) textareaRef.current?.focus();
	}, [isActive]);

	// setDraft replaces the composer text and keeps the tracked caret in sync;
	// every programmatic text change must go through it (or set both).
	const setDraft = useCallback((text: string) => {
		setInput(text);
		setCaret(text.length);
	}, []);

	// Start empty so a seed delivered while switching from an editor is applied
	// by the newly mounted chat panel, not mistaken for one it already consumed.
	const [prevSeed, setPrevSeed] = useState<Props["seed"]>(null);
	if (seed && seed !== prevSeed) {
		setPrevSeed(seed);
		setDraft(
			seed.append && input.trim() ? `${input}\n\n${seed.text}` : seed.text,
		);
		const seedFiles = seed.files;
		if (seedFiles?.length) {
			setFiles((current) => [...new Set([...current, ...seedFiles])]);
		}
	}

	useEffect(() => {
		if (!seed) return;
		historyIdxRef.current = null;
		textareaRef.current?.focus();
		onSeedConsumed?.(seed.nonce);
	}, [onSeedConsumed, seed]);

	const skillToken = slashTokenAt(input, caret);
	const tokenKey = skillToken
		? `${skillToken.start}:${skillToken.query}`
		: null;
	const [skillSelection, setSkillSelection] = useState<{
		token: string | null;
		index: number;
	}>({ token: tokenKey, index: 0 });
	const skillActive =
		skillSelection.token === tokenKey ? skillSelection.index : 0;
	const setSkillActive = useCallback(
		(index: number) => setSkillSelection({ token: tokenKey, index }),
		[tokenKey],
	);

	const tokenOpen = !!skillToken;
	const skills = useSkills(sessionId, tokenOpen);

	const skillQuery = skillToken ? skillToken.query.toLowerCase() : null;
	const skillMatches = useMemo(() => {
		if (skillQuery === null) return [];
		if (!skillQuery) return skills;
		return skills.filter(
			(s) =>
				s.name.toLowerCase().includes(skillQuery) ||
				(s.description ?? "").toLowerCase().includes(skillQuery),
		);
	}, [skills, skillQuery]);

	const activeSkill = Math.min(
		skillActive,
		Math.max(0, skillMatches.length - 1),
	);

	const showSkills = skillMatches.length > 0 && tokenKey !== dismissedToken;

	const history = useMemo(() => {
		const out: string[] = [];
		for (const e of entries) {
			const text = e.type === "user" ? e.content.trim() : "";
			if (text && out[out.length - 1] !== text) out.push(text);
		}
		return out;
	}, [entries]);

	const recallHistory = useCallback(
		(text: string) => {
			setDraft(text);
			requestAnimationFrame(() => {
				const ta = textareaRef.current;
				if (ta) ta.setSelectionRange(ta.value.length, ta.value.length);
			});
		},
		[setDraft],
	);

	useLayoutEffect(() => {
		if (restoredRef.current || entries.length === 0) return;
		restoredRef.current = true;
		if (submitPendingRef.current) return;
		const el = containerRef.current;
		if (el) writeScrollTop(el, el.scrollHeight);
	}, [entries, writeScrollTop]);

	useLayoutEffect(() => {
		const container = containerRef.current;
		const content = contentRef.current;
		const spacer = spacerRef.current;
		if (!container || !content || !spacer) return;

		if (submitPendingRef.current) {
			const last = entries[entries.length - 1];
			if (last?.type !== "user") return;
			const userEl = findEntryElement(content, last.id);
			if (!userEl) return;

			submitPendingRef.current = false;
			userScrolledRef.current = false;

			spacer.style.height = `${container.clientHeight}px`;
			const cRect = container.getBoundingClientRect();
			const uRect = userEl.getBoundingClientRect();
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

		if (phase === "idle") {
			pinRef.current = null;
			const belowUser = container.scrollHeight - pin.top - spacer.offsetHeight;
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

		if (userScrolledRef.current) return;
		if (Math.abs(container.scrollTop - pin.top) > 2) {
			writeScrollTop(container, pin.top);
		}
	}, [entries, phase, writeScrollTop]);

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

	const handleSubmit = useCallback(
		async (
			intent?: TurnInputIntent,
			overrideText?: string,
		): Promise<boolean> => {
			const text = (overrideText ?? input).trim();
			if ((!text && images.length === 0 && files.length === 0) || submitting) {
				return false;
			}
			submitPendingRef.current = true;
			setSubmitting(true);
			setSendError(null);
			const imageData =
				images.length > 0 ? images.map((i) => i.dataUrl) : undefined;
			let sent = false;
			try {
				if (editingQueueId && onUpdateQueued) {
					sent = await onUpdateQueued(
						editingQueueId,
						text,
						files.length > 0 ? files : undefined,
						imageData,
					);
				} else {
					const nextIntent =
						intent ?? (isActive && canSteer ? "steer" : "follow_up");
					sent = await onSend(
						text,
						files.length > 0 ? files : undefined,
						imageData,
						nextIntent,
					);
				}
			} catch {
				sent = false;
			} finally {
				setSubmitting(false);
			}
			if (!sent) {
				submitPendingRef.current = false;
				setSendError("Message was not accepted. Your draft has been kept.");
				return false;
			}
			setDraft("");
			setFiles([]);
			setImages([]);
			setEditingQueueId(null);
			historyIdxRef.current = null;
			historyDraftRef.current = "";
			textareaRef.current?.focus();
			return true;
		},
		[
			input,
			submitting,
			images,
			editingQueueId,
			onUpdateQueued,
			isActive,
			canSteer,
			onSend,
			files,
			setDraft,
		],
	);

	// selectSkill completes the slash token at the caret in place; only a lone
	// leading command without an input hint submits directly.
	const selectSkill = useCallback(
		(s: Skill) => {
			const tok = slashTokenAt(input, caret);
			if (!tok) return;

			const end = wordEndAt(input, caret);
			const whole = tok.start === 0 && end === input.length;
			const needsInput = !!s.input_hint;

			if (whole && !needsInput) {
				void handleSubmit(undefined, `/${s.name}`);
				return;
			}

			const insert = `/${s.name}`;
			const trailing = input.slice(end);
			const glue = trailing.startsWith(" ") ? "" : " ";
			setInput(input.slice(0, tok.start) + insert + glue + trailing);
			const pos = tok.start + insert.length + 1;
			setCaret(pos);
			requestAnimationFrame(() => {
				const ta = textareaRef.current;
				if (ta) {
					ta.focus();
					ta.setSelectionRange(pos, pos);
				}
			});
		},
		[input, caret, handleSubmit],
	);

	const handleKeyDown = useCallback(
		(e: React.KeyboardEvent) => {
			if (showSkills) {
				switch (e.key) {
					case "ArrowDown":
						e.preventDefault();
						setSkillActive(Math.min(activeSkill + 1, skillMatches.length - 1));
						return;
					case "ArrowUp":
						e.preventDefault();
						setSkillActive(Math.max(activeSkill - 1, 0));
						return;
					case "Enter":
					case "Tab": {
						e.preventDefault();
						const s = skillMatches[activeSkill];
						if (s) selectSkill(s);
						return;
					}
					case "Escape":
						e.preventDefault();
						setDismissedToken(tokenKey);
						return;
				}
			}
			if (e.key === "Enter" && !e.shiftKey) {
				e.preventDefault();
				void handleSubmit(e.altKey ? "follow_up" : undefined);
			}
			if (e.key === "Escape" && isActive) {
				onCancel();
			}
			if (e.key === "ArrowUp" && !editingQueueId && history.length > 0) {
				const ta = e.currentTarget as HTMLTextAreaElement;
				const onFirstLine = !ta.value
					.slice(0, ta.selectionStart)
					.includes("\n");
				if (onFirstLine) {
					e.preventDefault();
					const navigating = historyIdxRef.current !== null;
					const idx = navigating
						? Math.max(0, (historyIdxRef.current as number) - 1)
						: history.length - 1;
					if (!navigating) historyDraftRef.current = input;
					historyIdxRef.current = idx;
					recallHistory(history[idx]);
				}
			}
			if (e.key === "ArrowDown" && historyIdxRef.current !== null) {
				const ta = e.currentTarget as HTMLTextAreaElement;
				const onLastLine = !ta.value.slice(ta.selectionEnd).includes("\n");
				if (onLastLine) {
					e.preventDefault();
					const idx = (historyIdxRef.current as number) + 1;
					if (idx >= history.length) {
						historyIdxRef.current = null;
						recallHistory(historyDraftRef.current);
					} else {
						historyIdxRef.current = idx;
						recallHistory(history[idx]);
					}
				}
			}
		},
		[
			handleSubmit,
			isActive,
			onCancel,
			showSkills,
			skillMatches,
			activeSkill,
			selectSkill,
			setSkillActive,
			tokenKey,
			editingQueueId,
			history,
			input,
			recallHistory,
		],
	);

	const editPendingInput = useCallback(
		(item: PendingTurnInput) => {
			setDraft(item.text);
			setFiles(item.files);
			setImages(
				item.images.map((dataUrl) => ({ id: crypto.randomUUID(), dataUrl })),
			);
			setEditingQueueId(item.state === "queued" ? item.id : null);
			setSendError(null);
			textareaRef.current?.focus();
		},
		[setDraft],
	);

	const addFile = useCallback((path: string) => {
		setFiles((prev) => (prev.includes(path) ? prev : [...prev, path]));
		setShowPicker(false);
		textareaRef.current?.focus();
	}, []);

	const removeFile = useCallback((path: string) => {
		setFiles((prev) => prev.filter((p) => p !== path));
	}, []);

	const addImageFiles = useCallback(async (fileList: FileList | File[]) => {
		const next: PendingImage[] = [];
		for (const f of Array.from(fileList)) {
			if (!f.type.startsWith("image/")) continue;
			try {
				const dataUrl = await processImage(f);
				next.push({ id: crypto.randomUUID(), dataUrl, name: f.name });
			} catch {}
		}
		if (next.length > 0) setImages((prev) => [...prev, ...next]);
	}, []);

	const removeImage = useCallback((id: string) => {
		setImages((prev) => prev.filter((i) => i.id !== id));
	}, []);

	const [dragOver, setDragOver] = useState(false);
	const dragDepthRef = useRef(0);

	const hasDragPayload = (e: React.DragEvent) => {
		const types = e.dataTransfer?.types;
		if (!types) return false;
		return (
			types.includes("Files") || types.includes("application/x-wingman-file")
		);
	};

	const handleDragEnter = useCallback((e: React.DragEvent) => {
		if (!hasDragPayload(e)) return;
		e.preventDefault();
		dragDepthRef.current++;
		setDragOver(true);
	}, []);

	const handleDragOver = useCallback((e: React.DragEvent) => {
		if (!hasDragPayload(e)) return;
		e.preventDefault();
	}, []);

	const handleDragLeave = useCallback((e: React.DragEvent) => {
		if (!hasDragPayload(e)) return;
		dragDepthRef.current = Math.max(0, dragDepthRef.current - 1);
		if (dragDepthRef.current === 0) setDragOver(false);
	}, []);

	const handleDrop = useCallback(
		(e: React.DragEvent) => {
			if (!hasDragPayload(e)) return;
			e.preventDefault();
			dragDepthRef.current = 0;
			setDragOver(false);
			const path = e.dataTransfer.getData("application/x-wingman-file");
			if (path) {
				addFile(path);
				return;
			}
			if (e.dataTransfer.files?.length) {
				void addImageFiles(e.dataTransfer.files);
				textareaRef.current?.focus();
			}
		},
		[addFile, addImageFiles],
	);

	const handlePaste = useCallback(
		(e: React.ClipboardEvent<HTMLTextAreaElement>) => {
			const items = e.clipboardData?.items;
			if (!items) return;
			const pasted: File[] = [];
			for (const item of items) {
				if (item.kind !== "file") continue;
				if (!item.type.startsWith("image/")) continue;
				const f = item.getAsFile();
				if (f) pasted.push(f);
			}
			if (pasted.length === 0) return;
			e.preventDefault();
			void addImageFiles(pasted);
		},
		[addImageFiles],
	);

	return (
		<div
			className="h-full relative overflow-hidden bg-bg"
			onDragEnter={handleDragEnter}
			onDragOver={handleDragOver}
			onDragLeave={handleDragLeave}
			onDrop={handleDrop}
		>
			<ScrollSnapshot
				phase={phase}
				capture={() => {
					if (userScrolledRef.current) captureAnchorForTurns(turns);
				}}
				restore={applyPendingAnchor}
			/>
			{dragOver && (
				<div className="absolute inset-2 z-30 pointer-events-none rounded-lg border-2 border-dashed border-accent bg-accent/5 flex items-center justify-center">
					<span className="text-[12px] text-fg-muted bg-bg/80 px-3 py-1 rounded">
						Drop to attach
					</span>
				</div>
			)}
			<div
				data-chat-history
				className={`h-full overflow-y-auto [overflow-anchor:none] ${historyPadding}`}
				ref={containerRef}
			>
				{loading && entries.length === 0 ? (
					<div className="h-full flex items-center justify-center">
						<Loader2 size={16} className="text-fg-dim animate-spin" />
					</div>
				) : loadError ? (
					<div className="h-full flex items-center justify-center">
						<div className="max-w-sm px-4 text-center text-[13px] text-danger break-words">
							{loadError}
						</div>
					</div>
				) : entries.length === 0 && phase === "idle" ? (
					<div className="flex h-full items-center justify-center px-4">
						<div className="flex max-w-sm flex-col items-center text-center">
							<img
								src={scheme === "light" ? "/icon_light.svg" : "/icon_dark.svg"}
								alt="Wingman"
								className="w-20 h-20 mb-4 opacity-80"
							/>
							<div className="text-[13px] text-fg-dim leading-relaxed">
								Ask me to write code, fix bugs, explore files, or run commands.
							</div>
						</div>
					</div>
				) : (
					<div className="mx-auto w-full max-w-4xl px-4 py-4" ref={contentRef}>
						<ToolProgressContext.Provider value={toolProgress ?? {}}>
							{turns.map((turn, idx) => {
								const isLastTurn = idx === turns.length - 1;
								const isActive = isLastTurn && phase !== "idle";
								return (
									<TurnView
										key={turn.key}
										turn={turn}
										isActive={isActive}
										phase={phase}
										applyPendingAnchor={applyPendingAnchor}
										onOpenFile={onOpenFile}
									/>
								);
							})}
						</ToolProgressContext.Provider>
					</div>
				)}
				<div ref={spacerRef} aria-hidden style={{ height: 0 }} />
			</div>

			<div className="absolute bottom-0 left-0 right-0 z-20">
				<div className="h-6 bg-gradient-to-t from-bg to-transparent pointer-events-none" />
				<div className="mx-auto w-full max-w-4xl bg-bg px-4 pb-3">
					{showQueue && (
						<TurnQueue
							items={pendingInputs}
							paused={queuePaused}
							onEdit={editPendingInput}
							onRemove={onRemoveQueued}
							onResume={onResumeQueue}
							onClear={onClearQueue}
						/>
					)}
					{inputError && (
						<div className="mb-1.5 flex items-start gap-2 rounded border border-danger/40 bg-danger/5 px-2 py-1 text-[11px] text-danger">
							<span className="min-w-0 flex-1 break-words">{inputError}</span>
							<button
								type="button"
								className="shrink-0 opacity-70 hover:opacity-100"
								onClick={() => {
									setSendError(null);
									onDismissError?.();
								}}
								aria-label="Dismiss error"
							>
								<X size={12} />
							</button>
						</div>
					)}
					{prompts.length > 0 && onPromptReply ? (
						<>
							{prompts.map((prompt) => (
								<PromptBar
									key={prompt.id}
									prompt={prompt}
									onReply={(reply) => onPromptReply(prompt.id, reply)}
								/>
							))}
						</>
					) : (
						<div
							ref={setComposer}
							data-chat-composer
							className="relative rounded-xl"
						>
							{editingQueueId && (
								<div className="flex items-center justify-between px-2.5 pt-2 text-[10px] text-warning font-mono">
									<span>Editing queued message</span>
									<button
										type="button"
										className="text-fg-dim hover:text-fg"
										onClick={() => setEditingQueueId(null)}
									>
										Cancel
									</button>
								</div>
							)}
							{showSkills && (
								<SkillPicker
									anchor={composer}
									skills={skillMatches}
									active={activeSkill}
									onSelect={selectSkill}
									onHover={setSkillActive}
									onClose={() => setDismissedToken(tokenKey)}
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

							{images.length > 0 && (
								<div className="flex flex-wrap gap-1.5 px-2.5 pt-2">
									{images.map((img) => (
										<span
											key={img.id}
											className="group relative inline-flex items-center rounded overflow-hidden bg-bg-active"
											title={img.name || "image"}
										>
											<img
												src={img.dataUrl}
												alt={img.name || "image"}
												className="block w-12 h-12 object-cover"
											/>
											<button
												type="button"
												className="absolute top-0.5 right-0.5 w-4 h-4 flex items-center justify-center rounded-full bg-bg/80 text-fg-dim hover:text-fg cursor-pointer"
												onClick={() => removeImage(img.id)}
												aria-label="Remove image"
											>
												<X size={10} />
											</button>
										</span>
									))}
								</div>
							)}

							<div className="px-3 pt-2">
								<textarea
									ref={textareaRef}
									autoFocus
									className="chat-composer-textarea field-sizing-content w-full appearance-none bg-transparent text-fg text-[12px] font-mono resize-none leading-[1.7] placeholder:text-fg-dim max-h-[40vh] overflow-y-auto"
									value={input}
									onChange={(e) => {
										historyIdxRef.current = null;
										setInput(e.target.value);
										setCaret(e.target.selectionStart);
									}}
									onSelect={(e) =>
										setCaret((e.target as HTMLTextAreaElement).selectionStart)
									}
									onKeyDown={handleKeyDown}
									onPaste={handlePaste}
									placeholder={placeholder}
									rows={1}
								/>
							</div>

							<div className="flex items-center justify-between px-1.5 pb-1.5 pt-1 gap-1">
								<div className="flex items-center gap-0 min-w-0">
									<div className="relative flex items-center">
										<button
											ref={setFilePickerButton}
											type="button"
											className="w-7 h-7 flex items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
											onClick={() => setShowPicker((s) => !s)}
											title="Add file context"
										>
											<Plus size={14} />
										</button>
										{showPicker && (
											<FilePicker
												anchor={filePickerButton}
												onSelect={addFile}
												onClose={() => setShowPicker(false)}
											/>
										)}
									</div>
									<input
										ref={imageInputRef}
										type="file"
										accept="image/*"
										multiple
										className="hidden"
										onChange={(e) => {
											if (e.target.files) void addImageFiles(e.target.files);
											e.target.value = "";
										}}
									/>
									<ModePicker
										modes={modes}
										current={mode}
										onSelect={onSelectMode}
									/>
									<ModelPicker sessionId={sessionId} />
								</div>

								<div className="flex items-center gap-0">
									<button
										type="button"
										className="w-7 h-7 flex items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
										onClick={() => imageInputRef.current?.click()}
										title="Attach image"
									>
										<Paperclip size={14} />
									</button>
									{(() => {
										const hasInput =
											input.trim() !== "" ||
											images.length > 0 ||
											files.length > 0;
										const mode: "send" | "stop" | "disabled" = hasInput
											? "send"
											: isActive
												? "stop"
												: "disabled";
										return (
											<>
												<button
													type="button"
													className={`group w-7 h-7 flex items-center justify-center rounded cursor-pointer transition-colors ${
														mode === "disabled"
															? "text-fg-dim opacity-40 cursor-not-allowed"
															: "text-fg-muted hover:text-fg hover:bg-bg-hover"
													}`}
													onClick={
														mode === "stop"
															? () => onCancel(false)
															: () => void handleSubmit()
													}
													disabled={mode === "disabled" || submitting}
													title={
														mode === "stop"
															? "Stop (Esc)"
															: editingQueueId
																? "Update queued message (Enter)"
																: mode === "send" && isActive && canSteer
																	? "Steer current turn (Enter) · Queue with Alt+Enter"
																	: mode === "send" && isActive
																		? "Queue follow-up (Enter)"
																		: "Send (Enter)"
													}
												>
													{mode === "stop" ? (
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
												{hasInput &&
													isActive &&
													canSteer &&
													!editingQueueId && (
														<button
															type="button"
															className="w-7 h-7 flex items-center justify-center rounded text-fg-dim hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
															onClick={() => void handleSubmit("follow_up")}
															title="Queue after current turn (Alt+Enter)"
														>
															<ListPlus size={14} />
														</button>
													)}
											</>
										);
									})()}
								</div>
							</div>
						</div>
					)}
				</div>
			</div>
		</div>
	);
}
