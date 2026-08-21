import { useQuery } from "@tanstack/react-query";
import { File } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { fileQueries } from "../api/files";
import { useDebouncedValue } from "../hooks/useDebouncedValue";
import { FloatingSurface } from "./ui/Floating";

interface Props {
	anchor: Element | null;
	onSelect: (path: string) => void;
	onClose: () => void;
}

export function FilePicker({ anchor, onSelect, onClose }: Props) {
	const [query, setQuery] = useState("");
	const [active, setActive] = useState(0);
	const inputRef = useRef<HTMLInputElement>(null);
	const debouncedQuery = useDebouncedValue(query, 80);
	const hits = useQuery(fileQueries.search(debouncedQuery)).data ?? [];

	useEffect(() => {
		inputRef.current?.focus();
	}, []);

	const onKey = (e: React.KeyboardEvent) => {
		if (e.key === "Escape") {
			e.preventDefault();
			onClose();
		} else if (e.key === "ArrowDown") {
			e.preventDefault();
			setActive((a) => Math.min(a + 1, hits.length - 1));
		} else if (e.key === "ArrowUp") {
			e.preventDefault();
			setActive((a) => Math.max(a - 1, 0));
		} else if (e.key === "Enter") {
			e.preventDefault();
			const h = hits[active];
			if (h) onSelect(h.path);
		}
	};

	return (
		<FloatingSurface
			open
			onOpenChange={(open) => !open && onClose()}
			reference={anchor}
			placement="top-start"
			role="dialog"
			label="Find a file"
			focusOnOpen
			className="z-[100] w-[360px] bg-bg-elevated/95 backdrop-blur-sm border border-border rounded-md shadow-xl overflow-hidden"
		>
			<div className="px-3 py-2 border-b border-border-subtle">
				<input
					ref={inputRef}
					type="text"
					value={query}
					onChange={(e) => {
						setQuery(e.target.value);
						setActive(0);
					}}
					onKeyDown={onKey}
					placeholder="Search files…"
					className="w-full bg-transparent text-fg text-[12px] outline-none placeholder:text-fg-dim"
				/>
			</div>
			<div className="max-h-[280px] overflow-y-auto py-1">
				{hits.length === 0 ? (
					<div className="px-3 py-4 text-[11px] text-fg-dim text-center">
						No files
					</div>
				) : (
					hits.map((h, i) => {
						const dir = h.path
							.slice(0, h.path.length - h.name.length)
							.replace(/\/$/, "");
						const isActive = i === active;
						return (
							<button
								type="button"
								key={h.path}
								onClick={() => onSelect(h.path)}
								onMouseEnter={() => setActive(i)}
								className={`w-full flex items-center gap-2 px-3 py-1.5 text-left cursor-pointer transition-colors ${
									isActive
										? "bg-bg-active text-fg"
										: "text-fg-muted hover:bg-bg-hover"
								}`}
							>
								<File size={12} className="text-fg-dim shrink-0" />
								<span className="truncate font-mono text-[12px]">{h.name}</span>
								{dir && (
									<span className="ml-auto truncate text-fg-dim font-mono text-[10.5px]">
										{dir}
									</span>
								)}
							</button>
						);
					})
				)}
			</div>
		</FloatingSurface>
	);
}
