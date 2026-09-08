import { useMutation, useQuery } from "@tanstack/react-query";
import { File, MessageSquare, Sparkles } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { fileQueries, type FileHit } from "../api/files";
import { useWorkspace, type SettingsPatch } from "../state/workspaceContext.ts";
import type { SessionSettings } from "../state/sessionStore.ts";
import { sessionQueries, type SessionInfo } from "../api/sessions";
import { useDebouncedValue } from "../hooks/useDebouncedValue";
import { type Skill, useSkills } from "../hooks/useSkills";
import { ModelProviderIcon } from "./ModelProviderIcon";
import { useToast } from "./ui/Feedback";

export interface PaletteAction {
	id: string;
	label: string;
	hint?: string;
	icon?: React.ReactNode;
	run: () => void;
}

export type PaletteSkill = Skill;

interface Item {
	key: string;
	group: string;
	label: string;
	hint?: string;
	icon?: React.ReactNode;
	run: () => void;
}

const EMPTY_SESSIONS: SessionInfo[] = [];
const EMPTY_FILES: FileHit[] = [];

interface Props {
	sessionId?: string;
	settings: SessionSettings;
	setSettings: (patch: SettingsPatch) => Promise<void>;
	onClose: () => void;
	actions: PaletteAction[];
	onRunSkill: (skill: PaletteSkill) => void;
	onSelectSession: (id: string) => void;
	onOpenFile: (path: string) => void;
}

export function CommandPalette({
	sessionId,
	settings,
	setSettings,
	onClose,
	actions,
	onRunSkill,
	onSelectSession,
	onOpenFile,
}: Props) {
	const toast = useToast();
	const listId = useId();
	const [query, setQuery] = useState("");
	const [activeKey, setActiveKey] = useState<string | null>(null);
	const listRef = useRef<HTMLDivElement>(null);
	const skills = useSkills(sessionId);
	const { backend } = useWorkspace();
	const sessionList =
		useQuery(sessionQueries.list(backend)).data ?? EMPTY_SESSIONS;
	const models = settings.models;
	const currentModel = settings.model;
	const debouncedQuery = useDebouncedValue(query.trim(), 80);
	const fileQuery = useQuery({
		...fileQueries.search(debouncedQuery),
		enabled: debouncedQuery.length >= 2,
	});
	const files =
		debouncedQuery.length >= 2 && debouncedQuery === query.trim()
			? (fileQuery.data ?? EMPTY_FILES).slice(0, 8)
			: EMPTY_FILES;

	const switchModel = useMutation({
		mutationFn: (model: string) => setSettings({ model }),
		onError: (error) => {
			toast({
				title: "Could not change model",
				description: String(error),
				tone: "error",
			});
		},
	});

	const items = useMemo<Item[]>(() => {
		const q = query.trim().toLowerCase();
		const match = (...parts: (string | undefined)[]) =>
			!q || parts.some((p) => p?.toLowerCase().includes(q));
		const out: Item[] = [];
		for (const a of actions) {
			if (!match(a.label)) continue;
			out.push({
				key: `action:${a.id}`,
				group: "Actions",
				label: a.label,
				hint: a.hint,
				icon: a.icon,
				run: a.run,
			});
		}
		const modelHits = models.filter(
			(m) => m.id !== currentModel && match(`Switch to ${m.name}`, m.id),
		);
		for (const m of modelHits.slice(0, q ? modelHits.length : 5)) {
			out.push({
				key: `model:${m.id}`,
				group: "Model",
				label: `Switch to ${m.name}`,
				icon: (
					<ModelProviderIcon
						namespace={m.namespace}
						size={12}
						className="text-fg-dim shrink-0"
					/>
				),
				run: () => {
					switchModel.mutate(m.id);
				},
			});
		}
		for (const s of skills) {
			if (!match(s.name, s.description)) continue;
			out.push({
				key: `skill:${s.name}`,
				group: "Skills",
				label: `/${s.name}`,
				hint: s.description || s.input_hint,
				icon: <Sparkles size={12} className="text-fg-dim shrink-0" />,
				run: () => onRunSkill(s),
			});
		}
		const sessions = sessionList.filter((s) => match(s.title, s.id));
		for (const s of sessions.slice(0, q ? sessions.length : 6)) {
			out.push({
				key: `session:${s.id}`,
				group: "Sessions",
				label: s.title || "Untitled session",
				icon: <MessageSquare size={12} className="text-fg-dim shrink-0" />,
				run: () => onSelectSession(s.id),
			});
		}
		for (const f of files) {
			out.push({
				key: `file:${f.path}`,
				group: "Files",
				label: f.name,
				hint: f.path.slice(0, f.path.length - f.name.length).replace(/\/$/, ""),
				icon: <File size={12} className="text-fg-dim shrink-0" />,
				run: () => onOpenFile(f.path),
			});
		}
		return out;
	}, [
		query,
		actions,
		skills,
		sessionList,
		files,
		models,
		currentModel,
		onRunSkill,
		onSelectSession,
		onOpenFile,
		switchModel,
	]);

	const active = Math.max(
		0,
		items.findIndex((item) => item.key === activeKey),
	);
	const selectedKey = items[active]?.key;
	useEffect(() => {
		listRef.current
			?.querySelector(`[data-idx="${active}"]`)
			?.scrollIntoView({ block: "nearest" });
	}, [active, selectedKey]);

	const runItem = (item: Item) => {
		onClose();
		item.run();
	};

	const onKeyDown = (e: React.KeyboardEvent) => {
		if (e.nativeEvent.isComposing) return;
		if (e.key === "Escape") {
			e.preventDefault();
			onClose();
		} else if (e.key === "ArrowDown") {
			e.preventDefault();
			setActiveKey(items[Math.min(active + 1, items.length - 1)]?.key ?? null);
		} else if (e.key === "ArrowUp") {
			e.preventDefault();
			setActiveKey(items[Math.max(active - 1, 0)]?.key ?? null);
		} else if (e.key === "Enter") {
			e.preventDefault();
			const item = items[active];
			if (item) runItem(item);
		}
	};

	let lastGroup = "";

	return (
		<div
			className="absolute inset-0 z-130 flex items-start justify-center bg-bg/40 backdrop-blur-[1px]"
			onMouseDown={onClose}
			role="dialog"
			aria-modal="true"
			aria-label="Command palette"
		>
			<div
				className="mt-[12vh] w-[560px] max-w-[90vw] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-lg shadow-2xl overflow-hidden"
				onMouseDown={(e) => e.stopPropagation()}
			>
				<div className="px-3 py-2.5 border-b border-border-subtle">
					<input
						autoFocus
						autoCapitalize="none"
						autoComplete="off"
						autoCorrect="off"
						spellCheck={false}
						type="text"
						value={query}
						onChange={(event) => {
							setQuery(event.target.value);
							setActiveKey(null);
						}}
						onKeyDown={onKeyDown}
						placeholder="Type a command, session, skill or file…"
						role="combobox"
						aria-label="Search commands"
						aria-expanded="true"
						aria-controls={listId}
						aria-activedescendant={
							selectedKey ? `${listId}-${active}` : undefined
						}
						aria-autocomplete="list"
						className="w-full bg-transparent text-fg text-[13px] outline-none placeholder:text-fg-dim"
					/>
				</div>
				<div
					ref={listRef}
					id={listId}
					role="listbox"
					className="max-h-[45vh] overflow-y-auto py-1"
				>
					{items.length === 0 ? (
						<div className="px-3 py-6 text-[11px] text-fg-dim text-center">
							No matches
						</div>
					) : (
						items.map((item, i) => {
							const header =
								item.group !== lastGroup ? (
									<div className="px-3 pt-2 pb-1 text-[10px] font-medium uppercase tracking-wide text-fg-dim">
										{item.group}
									</div>
								) : null;
							lastGroup = item.group;
							const isActive = i === active;
							return (
								<div key={item.key}>
									{header}
									<button
										type="button"
										data-idx={i}
										id={`${listId}-${i}`}
										role="option"
										aria-selected={isActive}
										onClick={() => runItem(item)}
										onMouseMove={() => setActiveKey(item.key)}
										className={`w-full flex items-center gap-2 px-3 py-1.5 text-left cursor-pointer transition-colors ${
											isActive
												? "bg-bg-active text-fg"
												: "text-fg-muted hover:bg-bg-hover"
										}`}
									>
										{item.icon}
										<span className="truncate text-[12px]">{item.label}</span>
										{item.hint && (
											<span className="ml-auto truncate text-fg-dim font-mono text-[10.5px] max-w-[45%]">
												{item.hint}
											</span>
										)}
									</button>
								</div>
							);
						})
					)}
				</div>
			</div>
		</div>
	);
}
