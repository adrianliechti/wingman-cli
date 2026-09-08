import { useCallback, useMemo, useState } from "react";
import type { PendingPrompt, PromptReply } from "../../hooks/useWebSocket";
import type { PromptField } from "../../types/protocol";

function promptFieldValue(
	field: PromptField,
	selections: Record<string, string[]>,
	others: Record<string, string>,
): string[] {
	const other = (others[field.name] ?? "").trim();
	const picked = selections[field.name] ?? [];
	if (field.multiple) {
		return other ? [...picked, other] : picked;
	}
	return other ? [other] : picked;
}

function coercePromptValue(field: PromptField, value: string): unknown {
	if (field.type === "boolean") return value === "true";
	if (field.type === "number" || field.type === "integer") {
		const number = Number(value);
		return Number.isFinite(number) ? number : value;
	}
	return value;
}

export function PromptBar({
	prompt,
	onReply,
}: {
	prompt: PendingPrompt;
	onReply: (reply: PromptReply) => void;
}) {
	const allFields = useMemo(
		() => (prompt.kind === "ask" ? (prompt.fields ?? []) : []),
		[prompt.kind, prompt.fields],
	);
	const fields = useMemo(
		() => allFields.filter((field) => !field.custom_answer_for),
		[allFields],
	);
	const customFields = useMemo(() => {
		const result: Record<string, PromptField> = {};
		for (const field of allFields) {
			if (field.custom_answer_for) result[field.custom_answer_for] = field;
		}
		return result;
	}, [allFields]);
	const [selections, setSelections] = useState<Record<string, string[]>>(() => {
		const init: Record<string, string[]> = {};
		for (const f of fields) {
			const d = f.default != null ? String(f.default) : "";
			if (d && f.enum?.includes(d)) init[f.name] = [d];
		}
		return init;
	});
	const [others, setOthers] = useState<Record<string, string>>(() => {
		const init: Record<string, string> = {};
		for (const f of fields) {
			if (f.default != null && !f.enum?.length) {
				init[f.name] = String(f.default);
			}
		}
		for (const [question, field] of Object.entries(customFields)) {
			if (field.default != null) init[question] = String(field.default);
		}
		return init;
	});

	const accept = useCallback(
		(content: Record<string, unknown>) => {
			onReply({ action: "accept", content });
		},
		[onReply],
	);

	const decline = useCallback(() => {
		onReply({ action: "decline" });
	}, [onReply]);

	// A lone single-select question submits on click, like a menu.
	const instant =
		fields.length === 1 &&
		(fields[0].enum?.length ?? 0) > 0 &&
		!fields[0].multiple;

	const complete =
		fields.length > 0 &&
		fields.every(
			(f) => !f.required || promptFieldValue(f, selections, others).length > 0,
		);

	const submit = useCallback(() => {
		const content: Record<string, unknown> = {};
		for (const f of fields) {
			const other = (others[f.name] ?? "").trim();
			const custom = customFields[f.name];
			if (other && custom) {
				content[custom.name] = coercePromptValue(custom, other);
				continue;
			}
			const value = promptFieldValue(f, selections, others);
			if (value.length === 0) continue;
			content[f.name] = f.multiple ? value : coercePromptValue(f, value[0]);
		}
		accept(content);
	}, [fields, selections, others, customFields, accept]);

	const [activeIdx, setActiveIdx] = useState(0);

	const advanceFrom = useCallback(
		(
			fromIdx: number,
			sel: Record<string, string[]>,
			oth: Record<string, string>,
		) => {
			for (let step = 1; step < fields.length; step++) {
				const i = (fromIdx + step) % fields.length;
				if (promptFieldValue(fields[i], sel, oth).length === 0) {
					setActiveIdx(i);
					return;
				}
			}
		},
		[fields],
	);

	const toggleOption = useCallback(
		(field: PromptField, option: string) => {
			if (instant) {
				accept({ [field.name]: option });
				return;
			}
			const current = selections[field.name] ?? [];
			let next: string[];
			let nextOthers = others;
			if (field.multiple) {
				next = current.includes(option)
					? current.filter((o) => o !== option)
					: [...current, option];
			} else {
				next = current[0] === option ? [] : [option];
				// Picking an option supersedes an abandoned free-text draft;
				// otherwise the stale draft would silently win on submit.
				if ((others[field.name] ?? "") !== "") {
					nextOthers = { ...others, [field.name]: "" };
					setOthers(nextOthers);
				}
			}
			const nextSelections = { ...selections, [field.name]: next };
			setSelections(nextSelections);

			if (!field.multiple && next.length > 0 && fields.length > 1) {
				const idx = fields.findIndex((f) => f.name === field.name);
				advanceFrom(idx, nextSelections, nextOthers);
			}
		},
		[instant, accept, selections, others, fields, advanceFrom],
	);

	// Instant mode's free-text path, shared by the Enter key and the Send button.
	const submitOther = useCallback(() => {
		const f = fields[0];
		if (!f) return;
		const value = (others[f.name] ?? "").trim();
		if (!value) return;
		const target = customFields[f.name] ?? f;
		accept({ [target.name]: coercePromptValue(target, value) });
	}, [fields, others, customFields, accept]);

	const declineButton = (
		<button
			type="button"
			className="px-3 h-7 flex items-center justify-center rounded text-[11px] text-fg-dim hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
			onClick={decline}
		>
			Decline
		</button>
	);

	const tabbed = prompt.kind === "ask" && fields.length > 1;
	const visibleFields = tabbed ? [fields[activeIdx]] : fields;

	return (
		<div className="relative rounded-lg border border-warning bg-bg-surface/60 flex flex-col">
			{tabbed ? (
				<div className="flex items-center gap-1 flex-wrap px-2 pt-2">
					{fields.map((f, i) => {
						const answered = promptFieldValue(f, selections, others).length > 0;
						return (
							<button
								key={f.name}
								type="button"
								className={`px-2 h-6 flex items-center rounded border text-[10px] font-mono uppercase tracking-wide cursor-pointer transition-colors ${
									i === activeIdx
										? "border-warning text-fg bg-bg-hover"
										: answered
											? "border-border text-success hover:text-fg"
											: "border-border text-fg-muted hover:text-fg"
								}`}
								onClick={() => setActiveIdx(i)}
							>
								{answered ? "✓ " : ""}
								{f.title || `Q${i + 1}`}
							</button>
						);
					})}
				</div>
			) : null}
			<div className="min-h-0 max-h-[50vh] overflow-y-auto overscroll-contain">
				{prompt.message ? (
					<div className="px-3 pt-2 pb-1 flex items-start gap-2">
						<span className="text-warning font-mono text-[12px] leading-[1.7] shrink-0">
							?
						</span>
						<span className="text-fg font-mono text-[12px] leading-[1.7] whitespace-pre-wrap break-words">
							{prompt.message}
						</span>
					</div>
				) : null}
				{prompt.kind === "ask" && fields.length > 0 ? (
					<div className="px-3 pt-1 pb-1 flex flex-col gap-3">
						{visibleFields.map((f) => (
							<PromptQuestion
								key={f.name}
								field={f}
								hideTitle={tabbed}
								selected={selections[f.name] ?? []}
								other={others[f.name] ?? ""}
								onToggle={(option) => toggleOption(f, option)}
								onOther={(v) => setOthers((prev) => ({ ...prev, [f.name]: v }))}
								onSubmit={
									instant
										? submitOther
										: () => {
												if (complete) {
													submit();
												} else if (tabbed) {
													advanceFrom(activeIdx, selections, others);
												}
											}
								}
							/>
						))}
					</div>
				) : null}
			</div>
			{prompt.kind !== "ask" ? (
				<div className="flex items-center justify-end gap-1 px-1.5 pb-1.5 pt-1">
					<button
						type="button"
						className="px-3 h-7 flex items-center justify-center rounded text-[11px] text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
						onClick={() => onReply({ action: "decline" })}
					>
						Deny
					</button>
					<button
						type="button"
						className="px-3 h-7 flex items-center justify-center rounded text-[11px] text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors"
						onClick={() => onReply({ action: "accept", scope: "session" })}
						title="Approve and don't ask again in this session"
					>
						Always allow
					</button>
					<button
						type="button"
						className="px-3 h-7 flex items-center justify-center rounded text-[11px] text-bg bg-success hover:opacity-90 cursor-pointer transition-opacity"
						onClick={() => onReply({ action: "accept" })}
					>
						Approve
					</button>
				</div>
			) : fields.length === 0 ? (
				<div className="flex items-center justify-end gap-1 px-1.5 pb-1.5 pt-1">
					{declineButton}
					<button
						type="button"
						className="px-3 h-7 flex items-center justify-center rounded text-[11px] text-bg bg-success hover:opacity-90 cursor-pointer transition-opacity"
						onClick={() => accept({})}
					>
						Accept
					</button>
				</div>
			) : (
				<div className="flex items-center justify-end gap-1 px-1.5 pb-1.5 pt-1 border-t border-border/60">
					{declineButton}
					{instant ? (
						<button
							type="button"
							className="px-3 h-7 flex items-center justify-center rounded text-[11px] text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
							onClick={submitOther}
							disabled={(others[fields[0].name] ?? "").trim() === ""}
							title="Submit (Enter)"
						>
							Send
						</button>
					) : (
						<button
							type="button"
							className="px-3 h-7 flex items-center justify-center rounded text-[11px] text-fg-muted hover:text-fg hover:bg-bg-hover cursor-pointer transition-colors disabled:opacity-40 disabled:cursor-not-allowed"
							onClick={submit}
							disabled={!complete}
						>
							Submit
						</button>
					)}
				</div>
			)}
		</div>
	);
}

function PromptQuestion({
	field,
	hideTitle,
	selected,
	other,
	onToggle,
	onOther,
	onSubmit,
}: {
	field: PromptField;
	hideTitle?: boolean;
	selected: string[];
	other: string;
	onToggle: (option: string) => void;
	onOther: (value: string) => void;
	onSubmit?: () => void;
}) {
	const [focused, setFocused] = useState<number | null>(null);

	const options = field.enum ?? [];
	const hasOptions = options.length > 0;

	const previewIndex =
		focused ?? (selected[0] ? options.indexOf(selected[0]) : -1);
	const preview =
		previewIndex >= 0 ? field.enum_previews?.[previewIndex] : undefined;

	const inputClass =
		"w-full bg-bg border border-border rounded px-2 py-1 text-fg text-[12px] font-mono outline-none focus:border-warning transition-colors placeholder:text-fg-dim";

	const handleKeyDown = (e: React.KeyboardEvent) => {
		if (e.nativeEvent.isComposing) return;
		if (e.key === "Enter" && !e.shiftKey && onSubmit) {
			e.preventDefault();
			onSubmit();
		}
	};

	return (
		<div className="flex flex-col gap-1">
			{(!hideTitle && field.title) || field.description ? (
				<div className="flex items-baseline gap-2">
					{!hideTitle && field.title ? (
						<span className="shrink-0 px-1.5 py-0.5 rounded bg-bg-hover text-fg-muted text-[10px] font-mono uppercase tracking-wide">
							{field.title}
						</span>
					) : null}
					{field.description ? (
						<span className="text-fg font-mono text-[12px] leading-[1.6] whitespace-pre-wrap break-words">
							{field.description}
						</span>
					) : null}
				</div>
			) : null}
			{hasOptions ? (
				<>
					<div className="flex flex-col gap-1">
						{options.map((option, i) => {
							const isSelected = selected.includes(option);
							return (
								<button
									key={`${i}-${option}`}
									type="button"
									className={`text-left px-2.5 py-1.5 rounded border cursor-pointer transition-colors ${
										isSelected
											? "border-warning bg-bg-hover"
											: "border-border hover:border-warning hover:bg-bg-hover"
									}`}
									onClick={() => onToggle(option)}
									onMouseEnter={() => setFocused(i)}
									onMouseLeave={() => setFocused(null)}
									onFocus={() => setFocused(i)}
									onBlur={() => setFocused(null)}
								>
									<span className="text-fg text-[12px] font-mono">
										{field.multiple ? (isSelected ? "☑ " : "☐ ") : ""}
										{option}
									</span>
									{field.enum_descriptions?.[i] ? (
										<span className="block text-fg-dim text-[11px] leading-snug">
											{field.enum_descriptions[i]}
										</span>
									) : null}
								</button>
							);
						})}
					</div>
					{preview ? (
						<pre className="mt-1 max-h-48 overflow-auto rounded border border-border bg-bg p-2 text-[11px] font-mono text-fg-muted whitespace-pre">
							{preview}
						</pre>
					) : null}
					{field.strict ? null : (
						<input
							autoFocus
							className={inputClass}
							type="text"
							value={other}
							onChange={(e) => onOther(e.target.value)}
							onKeyDown={handleKeyDown}
							placeholder="Other…"
						/>
					)}
				</>
			) : field.type === "boolean" ? (
				<select
					className={inputClass}
					value={other}
					onChange={(e) => onOther(e.target.value)}
				>
					<option value="">—</option>
					<option value="true">Yes</option>
					<option value="false">No</option>
				</select>
			) : (
				<input
					autoFocus
					className={inputClass}
					type={
						field.type === "number" || field.type === "integer"
							? "number"
							: "text"
					}
					value={other}
					onChange={(e) => onOther(e.target.value)}
					onKeyDown={handleKeyDown}
					placeholder="Type your answer…"
				/>
			)}
		</div>
	);
}
