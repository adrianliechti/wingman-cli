import { Loader2 } from "lucide-react";
import { useEffect, useState } from "react";
import { type GraphInsights, fetchGraphInsights } from "../../api/graph";

const SERIES_COLORS = [
	"var(--color-info)",
	"var(--color-purple)",
	"var(--color-orange)",
	"var(--color-success)",
];
const OTHERS_COLOR = "var(--color-border-strong)";
const DAY_LABELS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

function seriesColor(index: number, name: string) {
	if (name === "others") return OTHERS_COLOR;
	return SERIES_COLORS[index % SERIES_COLORS.length];
}

function relativeDay(value: string) {
	const then = new Date(value);
	const days = Math.floor((Date.now() - then.getTime()) / 86_400_000);
	if (days <= 0) return "today";
	if (days === 1) return "yesterday";
	if (days < 14) return `${days}d ago`;
	if (days < 70) return `${Math.floor(days / 7)}w ago`;
	return then.toLocaleDateString();
}

function weekLabel(value: string) {
	return new Date(value).toLocaleDateString(undefined, {
		month: "short",
		day: "numeric",
	});
}

function Card({
	title,
	detail,
	className,
	children,
}: {
	title: string;
	detail?: string;
	className?: string;
	children: React.ReactNode;
}) {
	return (
		<section
			className={`flex min-h-0 flex-col overflow-hidden rounded-md border border-border-subtle bg-bg-surface/10 ${className ?? ""}`}
		>
			<div className="flex shrink-0 items-baseline gap-2 border-b border-border-subtle px-2.5 py-1.5">
				<span className="text-[10px] font-medium uppercase tracking-wider text-fg-dim">
					{title}
				</span>
				{detail && (
					<span className="min-w-0 truncate text-[9px] text-fg-dim">
						{detail}
					</span>
				)}
			</div>
			<div className="min-h-0 overflow-y-auto">{children}</div>
		</section>
	);
}

function BarRow({
	label,
	detail,
	share,
	mono,
}: {
	label: string;
	detail: string;
	share: number;
	mono?: boolean;
}) {
	return (
		<div className="border-b border-border-subtle/60 px-2.5 py-1.5 last:border-b-0">
			<div className="flex items-baseline justify-between gap-2 text-[11px]">
				<span className={`truncate text-fg-muted ${mono ? "font-mono" : ""}`}>
					{label}
				</span>
				<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
					{detail}
				</span>
			</div>
			<div className="mt-1 h-1 overflow-hidden rounded-full bg-bg-active">
				<div
					className="h-full rounded-full bg-accent/60"
					style={{ width: `${Math.max(2, share * 100)}%` }}
				/>
			</div>
		</div>
	);
}

export function ActivityView({
	onOpenFile,
}: {
	onOpenFile: (path: string, line?: number) => void;
}) {
	const [insights, setInsights] = useState<GraphInsights | null>(null);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		const controller = new AbortController();
		fetchGraphInsights(controller.signal)
			.then(setInsights)
			.catch((loadError: unknown) => {
				if (controller.signal.aborted) return;
				setError(
					loadError instanceof Error ? loadError.message : "Load failed",
				);
			});
		return () => controller.abort();
	}, []);

	if (error) {
		return (
			<div className="grid h-full place-items-center px-6 text-center text-[11px] text-fg-dim">
				<div>
					<div className="mb-1 text-fg-muted">Git history unavailable</div>
					{error}
				</div>
			</div>
		);
	}
	if (!insights) {
		return (
			<div className="flex h-full items-center justify-center gap-1.5 text-[11px] text-fg-dim">
				<Loader2 size={11} className="animate-spin" />
				<span>Reading git history…</span>
			</div>
		);
	}

	const weeks = insights.weeks;
	const series = insights.author_weeks;
	const maxWeek = Math.max(1, ...weeks.map((w) => w.commits));
	const maxAuthor = Math.max(1, ...insights.authors.map((a) => a.commits));
	const maxModule = Math.max(1, ...insights.modules.map((m) => m.commits));
	const maxPunch = Math.max(1, ...insights.punch.flat());

	return (
		<div className="@container h-full overflow-y-auto p-3">
			<div className="mb-3 text-[11px] text-fg-muted">
				{insights.commits.toLocaleString()} commits · {insights.authors.length}{" "}
				{insights.authors.length === 1 ? "contributor" : "contributors"}
				{insights.since
					? ` · since ${new Date(insights.since).toLocaleDateString()}`
					: ""}
			</div>
			<div className="grid grid-cols-1 gap-3 @3xl:grid-cols-2">
				<Card title="Commits per week" detail={`last ${weeks.length} weeks`}>
					<div className="px-2.5 pt-3 pb-2">
						<div className="flex h-24 items-end gap-[2px] border-b border-border">
							{weeks.map((week, index) => {
								const breakdown = series
									.map((s) => ({ name: s.name, count: s.weeks[index] ?? 0 }))
									.filter((s) => s.count > 0);
								return (
									<div
										key={week.week}
										title={[
											`${weekLabel(week.week)} · ${week.commits} ${week.commits === 1 ? "commit" : "commits"}`,
											...breakdown.map((s) => `${s.name}: ${s.count}`),
										].join("\n")}
										className="flex h-full flex-1 items-end"
									>
										{week.commits > 0 && (
											<div
												className="flex w-full flex-col-reverse gap-[1px] overflow-hidden rounded-t-[3px]"
												style={{
													height: `${Math.max(4, (week.commits / maxWeek) * 100)}%`,
												}}
											>
												{series.map((s, seriesIndex) => {
													const count = s.weeks[index] ?? 0;
													if (count === 0) return null;
													return (
														<div
															key={s.name}
															className="w-full"
															style={{
																flexGrow: count,
																background: seriesColor(seriesIndex, s.name),
																opacity: 0.75,
															}}
														/>
													);
												})}
											</div>
										)}
									</div>
								);
							})}
						</div>
						<div className="flex justify-between pt-1 text-[9px] text-fg-dim">
							<span>{weekLabel(weeks[0]?.week ?? "")}</span>
							<span>{weekLabel(weeks[weeks.length - 1]?.week ?? "")}</span>
						</div>
						{series.length > 1 && (
							<div className="flex flex-wrap gap-x-2.5 gap-y-1 pt-1.5">
								{series.map((s, index) => (
									<span
										key={s.name}
										className="flex items-center gap-1 text-[10px] text-fg-muted"
									>
										<span
											className="h-2 w-2 rounded-full"
											style={{
												background: seriesColor(index, s.name),
												opacity: 0.85,
											}}
										/>
										{s.name}
									</span>
								))}
							</div>
						)}
					</div>
				</Card>
				<Card title="Commit times" detail="by weekday and hour">
					<div className="px-2.5 pt-3 pb-2">
						{insights.punch.map((hours, day) => (
							<div
								key={DAY_LABELS[day]}
								className="mb-[2px] grid grid-cols-[28px_repeat(24,minmax(0,1fr))] gap-[2px]"
							>
								<span className="self-center text-[9px] leading-none text-fg-dim">
									{DAY_LABELS[day]}
								</span>
								{hours.map((count, hour) => (
									<div
										key={hour}
										title={`${DAY_LABELS[day]} ${hour}:00 · ${count} ${count === 1 ? "commit" : "commits"}`}
										className={`aspect-square rounded-[2px] ${count > 0 ? "bg-accent" : "bg-bg-active/50"}`}
										style={
											count > 0
												? { opacity: 0.2 + 0.8 * (count / maxPunch) }
												: undefined
										}
									/>
								))}
							</div>
						))}
						<div className="grid grid-cols-[28px_repeat(24,minmax(0,1fr))] gap-[2px] pt-0.5">
							<span />
							{Array.from({ length: 24 }, (_, hour) => (
								<span
									key={hour}
									className="text-center text-[8px] leading-none text-fg-dim"
								>
									{hour % 6 === 0 ? hour : ""}
								</span>
							))}
						</div>
					</div>
				</Card>
				<Card title="Contributors" detail="by commits in window">
					{insights.authors.map((author) => (
						<BarRow
							key={author.name}
							label={author.name}
							detail={`${author.commits} ${author.commits === 1 ? "commit" : "commits"} · ${author.files} files · ${relativeDay(author.last)}`}
							share={author.commits / maxAuthor}
						/>
					))}
				</Card>
				<Card title="Where work happens" detail="commits touching each area">
					{insights.modules.map((module) => (
						<BarRow
							key={module.module}
							label={module.module}
							detail={`${module.commits} ${module.commits === 1 ? "commit" : "commits"}`}
							share={module.commits / maxModule}
							mono
						/>
					))}
					{insights.modules.length === 0 && (
						<div className="px-3 py-4 text-center text-[11px] text-fg-dim">
							No file changes in the window
						</div>
					)}
				</Card>
				<Card
					className="@3xl:col-span-2"
					title="Most changed files"
					detail="commits in window"
				>
					{insights.churn.map((entry) => (
						<button
							key={entry.file}
							type="button"
							title={`Open ${entry.file}`}
							onClick={() => onOpenFile(entry.file)}
							className="group flex w-full items-baseline gap-1.5 border-b border-border-subtle/60 px-2.5 py-1 text-left text-[11px] last:border-b-0 hover:bg-bg-hover"
						>
							<span className="min-w-0 flex-1 truncate font-mono text-fg-muted group-hover:text-fg">
								{entry.file}
							</span>
							<span className="shrink-0 text-[10px] text-fg-dim tabular-nums">
								{entry.commits} {entry.commits === 1 ? "commit" : "commits"} ·{" "}
								{entry.authors} {entry.authors === 1 ? "author" : "authors"}
							</span>
						</button>
					))}
					{insights.churn.length === 0 && (
						<div className="px-3 py-4 text-center text-[11px] text-fg-dim">
							No file changes in the window
						</div>
					)}
				</Card>
			</div>
		</div>
	);
}
