import {
	ArrowUp,
	ChevronDown,
	ChevronRight,
	LoaderCircle,
	Plus,
	Square,
	X,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
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

export function ChatPanel({ entries, phase, onSend, onCancel }: Props) {
	const [input, setInput] = useState("");
	const [files, setFiles] = useState<string[]>([]);
	const [showPicker, setShowPicker] = useState(false);
	const messagesRef = useRef<HTMLDivElement>(null);
	const textareaRef = useRef<HTMLTextAreaElement>(null);

	const isActive = phase !== "idle";

	// Show the skill picker while the user is still typing the slash command
	// (no whitespace yet — once they add a space we treat the rest as args).
	const skillMatch = input.match(/^\/(\S*)$/);
	const showSkills = !!skillMatch && !isActive;
	const skillQuery = skillMatch ? skillMatch[1] : "";

	useEffect(() => {
		const el = messagesRef.current;
		if (el) el.scrollTop = el.scrollHeight;
	}, []);

	const handleSubmit = useCallback(() => {
		const text = input.trim();
		if (!text || isActive) return;
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
				onSend(`/${s.name}`, files.length > 0 ? files : undefined);
				setInput("");
				setFiles([]);
			}
		},
		[isActive, onSend, files],
	);

	return (
		<div className="h-full relative overflow-hidden bg-bg">
			<div className="h-full overflow-y-auto pb-24" ref={messagesRef}>
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
						{phase !== "idle" && phase !== "streaming" && (
							<PhaseIndicator phase={phase} />
						)}
					</div>
				)}
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
									isActive
										? "bg-fg-muted text-bg hover:bg-fg"
										: input.trim()
											? "bg-fg-muted text-bg hover:bg-fg"
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
