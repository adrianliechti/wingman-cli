import { Sparkles } from "lucide-react";
import { FloatingSurface } from "./ui/Floating";

export interface Skill {
	name: string;
	description?: string;
	input_hint?: string;
}

interface Props {
	anchor: Element | null;
	skills: Skill[];
	active: number;
	onSelect: (skill: Skill) => void;
	onHover: (index: number) => void;
	onClose: () => void;
}

// SkillPicker renders the completion list for the slash token at the caret.
// The composer owns the data and keyboard navigation; this component only
// renders and reports mouse interactions.
export function SkillPicker({
	anchor,
	skills,
	active,
	onSelect,
	onHover,
	onClose,
}: Props) {
	if (skills.length === 0) return null;

	return (
		<FloatingSurface
			open
			onOpenChange={(open) => !open && onClose()}
			reference={anchor}
			placement="top-start"
			role="listbox"
			label="Skills"
			returnFocus={false}
			className="z-[100] w-[360px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl overflow-hidden"
		>
			<div className="max-h-[280px] overflow-y-auto py-1">
				{skills.map((s, i) => {
					const isActive = i === active;
					return (
						<button
							type="button"
							role="option"
							aria-selected={isActive}
							key={s.name}
							onClick={() => onSelect(s)}
							onMouseEnter={() => onHover(i)}
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
									{s.input_hint && (
										<span className="text-[10.5px] text-fg-dim font-mono truncate">
											{s.input_hint}
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
		</FloatingSurface>
	);
}
