import type { LucideIcon } from "lucide-react";

export function PanelEmptyState({
	icon: Icon,
	iconClassName = "text-fg-dim",
	title,
	hint,
}: {
	icon: LucideIcon;
	iconClassName?: string;
	title: string;
	hint?: string;
}) {
	return (
		<div className="flex flex-1 flex-col items-center justify-center px-4 py-8 text-center">
			<div className="flex h-9 w-9 items-center justify-center rounded-full border border-border-subtle bg-bg-surface/40">
				<Icon size={15} className={iconClassName} />
			</div>
			<div className="mt-2.5 text-[11px] font-medium text-fg-muted">
				{title}
			</div>
			{hint && (
				<div className="mt-1 max-w-52 text-[10px] leading-snug text-fg-dim">
					{hint}
				</div>
			)}
		</div>
	);
}
