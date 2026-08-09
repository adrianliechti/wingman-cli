import { Brain, File, MessageSquare, Sparkles } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useToast } from "./ui/Feedback";

export interface PaletteAction {
	id: string;
	label: string;
	hint?: string;
	icon?: React.ReactNode;
	run: () => void;
}

interface ModelInfo {
	id: string;
	name: string;
}

export interface PaletteSkill {
	name: string;
	description?: string;
	input_hint?: string;
}

interface SessionEntry {
	id: string;
	title?: string;
	updated_at: string;
}

interface FileHit {
	path: string;
	name: string;
}

interface Item {
	key: string;
	group: string;
	label: string;
	hint?: string;
	icon?: React.ReactNode;
	run: () => void;
}

interface Props {
	sessionId?: string;
	onClose: () => void;
	actions: PaletteAction[];
	onRunSkill: (skill: PaletteSkill) => void;
	onSelectSession: (id: string) => void;
	onOpenFile: (path: string) => void;
}

export function CommandPalette({
	sessionId,
	onClose,
	actions,
	onRunSkill,
	onSelectSession,
	onOpenFile,
}: Props) {
	const toast = useToast();
	const listId = useId();
	const [query, setQuery] = useState("");
	const [skills, setSkills] = useState<PaletteSkill[]>([]);
	const [sessionList, setSessionList] = useState<SessionEntry[]>([]);
	const [files, setFiles] = useState<FileHit[]>([]);
	const [models, setModels] = useState<ModelInfo[]>([]);
	const [currentModel, setCurrentModel] = useState("");
	const [active, setActive] = useState(0);
	const listRef = useRef<HTMLDivElement>(null);

	const apiBase = sessionId
		? `/api/sessions/${encodeURIComponent(sessionId)}`
		: "/api";

	useEffect(() => {
		const skillsURL = sessionId
			? `/api/skills?session=${encodeURIComponent(sessionId)}`
			: "/api/skills";
		fetch(skillsURL)
			.then((r) => (r.ok ? r.json() : []))
			.then((data: PaletteSkill[]) => setSkills(data ?? []))
			.catch(() => setSkills([]));
		fetch("/api/sessions")
			.then((r) => (r.ok ? r.json() : []))
			.then((data: SessionEntry[]) => setSessionList(data ?? []))
			.catch(() => setSessionList([]));
		fetch("/api/models")
			.then((r) => (r.ok ? r.json() : []))
			.then((data: ModelInfo[]) => setModels(data ?? []))
			.catch(() => setModels([]));
		fetch(`${apiBase}/model`)
			.then((r) => (r.ok ? r.json() : {}))
			.then((data: { model?: string }) => setCurrentModel(data.model || ""))
			.catch(() => {});
	}, [apiBase, sessionId]);

	useEffect(() => {
		const q = query.trim();
		if (q.length < 2) return;
		let cancelled = false;
		const t = setTimeout(() => {
			fetch(`/api/files/search?q=${encodeURIComponent(q)}`)
				.then((r) => (r.ok ? r.json() : []))
				.then((data: FileHit[]) => {
					if (!cancelled) setFiles((data ?? []).slice(0, 8));
				})
				.catch(() => {
					if (!cancelled) setFiles([]);
				});
		}, 80);
		return () => {
			cancelled = true;
			clearTimeout(t);
		};
	}, [query]);

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
			(m) => m.id !== currentModel && match(m.name, m.id),
		);
		for (const m of modelHits.slice(0, q ? modelHits.length : 5)) {
			out.push({
				key: `model:${m.id}`,
				group: "Model",
				label: `Switch to ${m.name}`,
				icon: <Brain size={12} className="text-fg-dim shrink-0" />,
				run: () => {
					void fetch(`${apiBase}/model`, {
						method: "POST",
						headers: { "Content-Type": "application/json" },
						body: JSON.stringify({ model: m.id }),
					})
						.then(async (response) => {
							if (!response.ok) throw new Error(await response.text());
						})
						.catch((error) => {
							toast({
								title: "Could not change model",
								description: String(error),
								tone: "error",
							});
						});
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
		apiBase,
		onRunSkill,
		onSelectSession,
		onOpenFile,
		toast,
	]);

	const [prevQuery, setPrevQuery] = useState(query);
	if (prevQuery !== query) {
		setPrevQuery(query);
		setActive(0);
		if (query.trim().length < 2 && files.length > 0) setFiles([]);
	}

	useEffect(() => {
		listRef.current
			?.querySelector(`[data-idx="${active}"]`)
			?.scrollIntoView({ block: "nearest" });
	}, [active]);

	const clamped = Math.min(active, Math.max(0, items.length - 1));

	const runItem = (item: Item) => {
		onClose();
		item.run();
	};

	const onKeyDown = (e: React.KeyboardEvent) => {
		if (e.key === "Escape") {
			e.preventDefault();
			onClose();
		} else if (e.key === "ArrowDown") {
			e.preventDefault();
			setActive(Math.min(clamped + 1, items.length - 1));
		} else if (e.key === "ArrowUp") {
			e.preventDefault();
			setActive(Math.max(clamped - 1, 0));
		} else if (e.key === "Enter") {
			e.preventDefault();
			const item = items[clamped];
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
						type="text"
						value={query}
						onChange={(e) => setQuery(e.target.value)}
						onKeyDown={onKeyDown}
						placeholder="Type a command, session, skill or file…"
						role="combobox"
						aria-label="Search commands"
						aria-expanded="true"
						aria-controls={listId}
						aria-activedescendant={
							items[clamped] ? `${listId}-${clamped}` : undefined
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
							const isActive = i === clamped;
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
										onMouseEnter={() => setActive(i)}
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
