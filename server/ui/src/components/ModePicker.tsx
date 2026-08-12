import { Compass, Wrench } from "lucide-react";
import { useRef, useState } from "react";
import { FloatingMenu } from "./ui/Floating";

export type ModeOption = { id: string; name: string; description?: string };

function isPlanLike(id: string): boolean {
	return /plan|read|only/i.test(id);
}

interface Props {
	modes: ModeOption[];
	current: string;
	onSelect: (id: string) => void;
}

export function ModePicker({ modes, current, onSelect }: Props) {
	const [open, setOpen] = useState(false);
	const btnRef = useRef<HTMLButtonElement>(null);

	if (modes.length === 0) return null;

	const active = modes.find((m) => m.id === current);
	const planLike = isPlanLike(current);
	const Icon = planLike ? Compass : Wrench;
	const label = active?.name ?? current ?? "Mode";

	return (
		<div className="relative">
			<button
				ref={btnRef}
				type="button"
				onClick={() => setOpen((v) => !v)}
				className={`flex items-center gap-1 px-2 h-7 rounded text-[11.5px] cursor-pointer transition-colors ${
					planLike
						? "text-warning hover:bg-bg-hover"
						: "text-fg-muted hover:text-fg hover:bg-bg-hover"
				}`}
				title={`Mode: ${label}`}
				aria-haspopup="menu"
				aria-expanded={open}
			>
				<Icon size={12} className="shrink-0" />
				<span>{label}</span>
			</button>
			<FloatingMenu
				open={open}
				onOpenChange={setOpen}
				reference={btnRef.current}
				placement="top-start"
				label="Session mode"
				className="z-[100] w-[320px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl py-1"
			>
				{modes.map((opt) => {
					const isActive = opt.id === current;
					const OptIcon = isPlanLike(opt.id) ? Compass : Wrench;
					return (
						<button
							type="button"
							role="menuitemradio"
							aria-checked={isActive}
							key={opt.id}
							onClick={() => {
								onSelect(opt.id);
								setOpen(false);
							}}
							className={`w-full flex items-start gap-2 px-3 py-2 text-left cursor-pointer transition-colors ${
								isActive
									? "bg-bg-active text-fg"
									: "text-fg-muted hover:bg-bg-hover hover:text-fg"
							}`}
						>
							<OptIcon
								size={13}
								className={`mt-0.5 shrink-0 ${isPlanLike(opt.id) ? "text-warning" : "text-fg-dim"}`}
							/>
							<div className="min-w-0 flex-1">
								<div className="text-[12px] font-medium">{opt.name}</div>
								{opt.description && (
									<div className="text-[11px] text-fg-dim mt-0.5">
										{opt.description}
									</div>
								)}
							</div>
						</button>
					);
				})}
			</FloatingMenu>
		</div>
	);
}
