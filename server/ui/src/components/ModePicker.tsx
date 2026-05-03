import { Compass, Wrench } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";

type Mode = "agent" | "plan";

const options: { value: Mode; label: string; description: string }[] = [
	{
		value: "agent",
		label: "Agent",
		description: "Full read/write access — implements changes.",
	},
	{
		value: "plan",
		label: "Plan",
		description: "Read-only — proposes a plan, doesn't edit code.",
	},
];

export function ModePicker() {
	const [mode, setMode] = useState<Mode>("agent");
	const [open, setOpen] = useState(false);
	const popRef = useRef<HTMLDivElement>(null);
	const btnRef = useRef<HTMLButtonElement>(null);

	useEffect(() => {
		fetch("/api/mode")
			.then((r) => r.json())
			.then((data) => setMode(data.mode === "plan" ? "plan" : "agent"))
			.catch(() => {});
	}, []);

	const select = useCallback(
		(next: Mode) => {
			fetch("/api/mode", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ mode: next }),
			})
				.then((r) => r.json())
				.then((data) => {
					const m: Mode = data.mode === "plan" ? "plan" : "agent";
					setMode(m);
				})
				.catch(() => {});
			setOpen(false);
		},
		[],
	);

	useEffect(() => {
		if (!open) return;
		const handler = (e: MouseEvent) => {
			const target = e.target as Node;
			if (
				popRef.current &&
				!popRef.current.contains(target) &&
				btnRef.current &&
				!btnRef.current.contains(target)
			) {
				setOpen(false);
			}
		};
		document.addEventListener("mousedown", handler);
		return () => document.removeEventListener("mousedown", handler);
	}, [open]);

	const Icon = mode === "plan" ? Compass : Wrench;
	const label = mode === "plan" ? "Plan" : "Agent";

	return (
		<div className="relative">
			<button
				ref={btnRef}
				type="button"
				onClick={() => setOpen((v) => !v)}
				className={`flex items-center gap-1 px-2 h-7 rounded text-[11.5px] cursor-pointer transition-colors ${
					mode === "plan"
						? "text-warning hover:bg-bg-hover"
						: "text-fg-muted hover:text-fg hover:bg-bg-hover"
				}`}
				title={`Mode: ${label}`}
			>
				<Icon size={12} className="shrink-0" />
				<span>{label}</span>
			</button>
			{open && (
				<div
					ref={popRef}
					className="absolute bottom-full mb-1 left-0 w-[320px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl py-1 z-50"
				>
					{options.map((opt) => {
						const active = opt.value === mode;
						const OptIcon = opt.value === "plan" ? Compass : Wrench;
						return (
							<button
								type="button"
								key={opt.value}
								onClick={() => select(opt.value)}
								className={`w-full flex items-start gap-2 px-3 py-2 text-left cursor-pointer transition-colors ${
									active
										? "bg-bg-active text-fg"
										: "text-fg-muted hover:bg-bg-hover hover:text-fg"
								}`}
							>
								<OptIcon
									size={13}
									className={`mt-0.5 shrink-0 ${opt.value === "plan" ? "text-warning" : "text-fg-dim"}`}
								/>
								<div className="min-w-0 flex-1">
									<div className="text-[12px] font-medium">{opt.label}</div>
									<div className="text-[11px] text-fg-dim mt-0.5">
										{opt.description}
									</div>
								</div>
							</button>
						);
					})}
				</div>
			)}
		</div>
	);
}
