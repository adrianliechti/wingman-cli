import { Sparkles } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

interface Skill {
	name: string;
	description?: string;
	when_to_use?: string;
	arguments?: string[];
}

interface Props {
	query: string;
	onSelect: (skill: Skill) => void;
	onClose: () => void;
}

export function SkillPicker({ query, onSelect, onClose }: Props) {
	const [skills, setSkills] = useState<Skill[]>([]);
	const [active, setActive] = useState(0);
	const containerRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		fetch("/api/skills")
			.then((r) => (r.ok ? r.json() : []))
			.then((data: Skill[]) => setSkills(data))
			.catch(() => setSkills([]));
	}, []);

	const filtered = useMemo(() => {
		const q = query.trim().toLowerCase();
		if (!q) return skills;
		return skills.filter(
			(s) =>
				s.name.toLowerCase().includes(q) ||
				(s.description ?? "").toLowerCase().includes(q),
		);
	}, [skills, query]);

	useEffect(() => {
		setActive(0);
	}, [query]);

	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") {
				e.preventDefault();
				onClose();
			} else if (e.key === "ArrowDown") {
				e.preventDefault();
				setActive((a) => Math.min(a + 1, filtered.length - 1));
			} else if (e.key === "ArrowUp") {
				e.preventDefault();
				setActive((a) => Math.max(a - 1, 0));
			} else if (e.key === "Enter" || e.key === "Tab") {
				const s = filtered[active];
				if (s) {
					e.preventDefault();
					onSelect(s);
				}
			}
		};
		document.addEventListener("keydown", onKey, true);
		return () => document.removeEventListener("keydown", onKey, true);
	}, [filtered, active, onSelect, onClose]);

	useEffect(() => {
		const onClick = (e: MouseEvent) => {
			if (
				containerRef.current &&
				!containerRef.current.contains(e.target as Node)
			) {
				onClose();
			}
		};
		document.addEventListener("mousedown", onClick);
		return () => document.removeEventListener("mousedown", onClick);
	}, [onClose]);

	if (filtered.length === 0) return null;

	return (
		<div
			ref={containerRef}
			className="absolute bottom-full mb-1 left-0 w-[360px] max-w-[90vw] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl z-50 overflow-hidden"
		>
			<div className="max-h-[280px] overflow-y-auto py-1">
				{filtered.map((s, i) => {
					const isActive = i === active;
					return (
						<button
							type="button"
							key={s.name}
							onClick={() => onSelect(s)}
							onMouseEnter={() => setActive(i)}
							className={`w-full flex items-start gap-2 px-3 py-1.5 text-left cursor-pointer transition-colors ${
								isActive
									? "bg-bg-active text-fg"
									: "text-fg-muted hover:bg-bg-hover"
							}`}
						>
							<Sparkles size={12} className="mt-0.5 text-fg-dim shrink-0" />
							<div className="min-w-0 flex-1">
								<div className="flex items-baseline gap-2">
									<span className="font-mono text-[12px] text-fg">
										/{s.name}
									</span>
									{s.arguments && s.arguments.length > 0 && (
										<span className="text-[10.5px] text-fg-dim font-mono truncate">
											{s.arguments.map((a) => `<${a}>`).join(" ")}
										</span>
									)}
								</div>
								{s.description && (
									<div className="text-[11px] text-fg-dim truncate mt-0.5">
										{s.description}
									</div>
								)}
							</div>
						</button>
					);
				})}
			</div>
		</div>
	);
}
