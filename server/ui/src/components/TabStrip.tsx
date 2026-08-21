import { useEffect, useRef, useState } from "react";
import type { CenterTab } from "../mainLayout";
import { Tab } from "./Tab";

export interface TabStripItem {
	tab: CenterTab;
	label: string;
	dirty: boolean;
	running: boolean;
	closable: boolean;
}

export function TabStrip({
	items,
	activeTabId,
	dragTabId,
	ariaLabel = "Open tabs",
	onActivate,
	onClose,
	onKeepOpen,
	onContextMenu,
	onDragStart,
	onDragEnd,
	onDropTab,
}: {
	items: TabStripItem[];
	activeTabId: string;
	dragTabId: string | null;
	ariaLabel?: string;
	onActivate: (tab: CenterTab) => void;
	onClose: (tab: CenterTab) => void;
	onKeepOpen: (id: string) => void;
	onContextMenu: (x: number, y: number, tabId: string | undefined) => void;
	onDragStart: (tabId: string) => void;
	onDragEnd: () => void;
	onDropTab: (index: number) => void;
}) {
	const listRef = useRef<HTMLDivElement>(null);
	const [indicator, setIndicator] = useState<{
		index: number;
		x: number;
	} | null>(null);

	useEffect(() => {
		const frame = requestAnimationFrame(() => {
			const list = listRef.current;
			const tab = list?.querySelector<HTMLElement>(
				`[data-center-tab="${CSS.escape(activeTabId)}"]`,
			);
			if (!list || !tab) return;
			const listRect = list.getBoundingClientRect();
			const tabRect = tab.getBoundingClientRect();
			if (tabRect.left < listRect.left) {
				list.scrollLeft -= listRect.left - tabRect.left;
			} else if (tabRect.right > listRect.right) {
				list.scrollLeft += tabRect.right - listRect.right;
			}
		});
		return () => cancelAnimationFrame(frame);
	}, [activeTabId, items.length]);

	const dropPosition = (clientX: number) => {
		const list = listRef.current;
		if (!list) return null;
		const listRect = list.getBoundingClientRect();
		const tabs = Array.from(
			list.querySelectorAll<HTMLElement>("[data-center-tab]"),
		);
		let index = tabs.length;
		let x = list.scrollLeft;
		for (let i = 0; i < tabs.length; i++) {
			const rect = tabs[i].getBoundingClientRect();
			if (clientX < rect.left + rect.width / 2) {
				index = i;
				x = rect.left - listRect.left + list.scrollLeft;
				break;
			}
			x = rect.right - listRect.left + list.scrollLeft;
		}
		return { index, x };
	};

	return (
		<div
			ref={listRef}
			className="tab-strip relative flex min-w-[80px] flex-1 items-stretch overflow-x-auto overscroll-x-contain scrollbar-none"
			role="tablist"
			aria-label={ariaLabel}
			onContextMenu={(event) => {
				event.preventDefault();
				const tabElement = (event.target as Element).closest<HTMLElement>(
					"[data-center-tab]",
				);
				onContextMenu(
					event.clientX,
					event.clientY,
					tabElement?.dataset.centerTab,
				);
			}}
			onDragOver={(event) => {
				if (!dragTabId) return;
				event.preventDefault();
				event.dataTransfer.dropEffect = "move";
				setIndicator(dropPosition(event.clientX));
			}}
			onDragLeave={(event) => {
				if (event.currentTarget.contains(event.relatedTarget as Node)) return;
				setIndicator(null);
			}}
			onDrop={(event) => {
				if (!dragTabId) return;
				event.preventDefault();
				const position = dropPosition(event.clientX);
				setIndicator(null);
				if (position) onDropTab(position.index);
			}}
		>
			{items.map((item, index) => {
				const { tab } = item;
				return (
					<Tab
						key={tab.id}
						id={tab.id}
						kind={tab.type}
						label={item.label}
						active={tab.id === activeTabId}
						preview={!!tab.preview}
						closable={item.closable}
						dirty={item.dirty}
						running={item.running}
						position={index}
						count={items.length}
						onActivate={() => onActivate(tab)}
						onNavigate={(next) => {
							const target = items[next];
							if (!target) return;
							onActivate(target.tab);
							requestAnimationFrame(() =>
								listRef.current
									?.querySelector<HTMLElement>(
										`[data-center-tab="${CSS.escape(target.tab.id)}"]`,
									)
									?.focus(),
							);
						}}
						onClose={() => onClose(tab)}
						onKeepOpen={() => onKeepOpen(tab.id)}
						onDragStart={() => onDragStart(tab.id)}
						onDragEnd={() => {
							setIndicator(null);
							onDragEnd();
						}}
					/>
				);
			})}
			{indicator && dragTabId && (
				<span
					aria-hidden="true"
					className="pointer-events-none absolute top-1 bottom-1 w-[2px] rounded-full bg-accent"
					style={{ left: `${indicator.x}px` }}
				/>
			)}
		</div>
	);
}

export type PaneZone = "left" | "right";

export function PaneDropZones({
	allowLeft,
	allowRight,
	onDrop,
}: {
	allowLeft: boolean;
	allowRight: boolean;
	onDrop: (zone: PaneZone) => void;
}) {
	const [zone, setZone] = useState<PaneZone | null>(null);
	const hostRef = useRef<HTMLDivElement>(null);

	const zoneAt = (clientX: number): PaneZone | null => {
		const rect = hostRef.current?.getBoundingClientRect();
		if (!rect || rect.width === 0) return null;
		if ((clientX - rect.left) / rect.width >= 0.75) {
			return allowRight ? "right" : null;
		}
		return allowLeft ? "left" : null;
	};

	const highlight: Record<PaneZone, string> = {
		right: "inset-y-0 right-0 w-1/2",
		left: "inset-y-0 left-0 w-1/2",
	};

	return (
		<div
			ref={hostRef}
			className="absolute inset-0 z-30"
			onDragOver={(event) => {
				event.preventDefault();
				const next = zoneAt(event.clientX);
				event.dataTransfer.dropEffect = next ? "move" : "none";
				setZone(next);
			}}
			onDragLeave={(event) => {
				if (event.currentTarget.contains(event.relatedTarget as Node)) return;
				setZone(null);
			}}
			onDrop={(event) => {
				event.preventDefault();
				const target = zoneAt(event.clientX);
				setZone(null);
				if (target) onDrop(target);
			}}
		>
			{zone && (
				<div
					aria-hidden="true"
					className={`pointer-events-none absolute rounded-sm border border-accent/60 bg-accent/15 transition-all duration-75 ${highlight[zone]}`}
				/>
			)}
		</div>
	);
}
