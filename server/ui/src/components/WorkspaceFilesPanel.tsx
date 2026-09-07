import { Lightbulb, Search, X } from "lucide-react";
import type { ReactNode } from "react";
import type { ServerMessage } from "../types/protocol";
import type { TabDisposition } from "../types/tabs";
import type { WorkspaceEditEnvelope } from "../workspaceEdit";
import { FileTree } from "./FileTree";
import { SearchPanel } from "./SearchPanel";

interface Props {
	workspaceName: string;
	headerContent?: ReactNode;
	searching: boolean;
	searchFocusKey: number;
	onSearch: () => void;
	onCloseSearch: () => void;
	onOpenInsights: () => void;
	onFileSelect: (path: string, disposition?: TabDisposition) => void;
	onFileMove: (from: string, to: string) => void;
	onOpenSearchResult: (
		path: string,
		line: number,
		column: number,
		disposition?: TabDisposition,
	) => void;
	onApplyWorkspaceEdit: (
		envelope: WorkspaceEditEnvelope,
		label: string,
	) => Promise<boolean>;
	platform?: string;
	subscribe?: (handler: (message: ServerMessage) => void) => () => void;
}

export function WorkspaceFilesPanel({
	workspaceName,
	headerContent,
	searching,
	searchFocusKey,
	onSearch,
	onCloseSearch,
	onOpenInsights,
	onFileSelect,
	onFileMove,
	onOpenSearchResult,
	onApplyWorkspaceEdit,
	platform,
	subscribe,
}: Props) {
	return (
		<div className="flex h-full min-h-0 flex-col overflow-hidden">
			<div className="flex h-9 shrink-0 items-center gap-2 px-3">
				{searching ? (
					<span className="min-w-0 flex-1 truncate text-[10px] font-medium uppercase tracking-wider text-fg-dim">
						Search
					</span>
				) : headerContent ? (
					headerContent
				) : (
					<span
						className="min-w-0 flex-1 truncate text-[10px] font-medium uppercase tracking-wider text-fg-dim"
						title={workspaceName}
					>
						{workspaceName || "Files"}
					</span>
				)}
				<button
					type="button"
					onClick={onOpenInsights}
					title="Open insights"
					aria-label="Open insights"
					className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
				>
					<Lightbulb size={12} />
				</button>
				<button
					type="button"
					onClick={searching ? onCloseSearch : onSearch}
					title={searching ? "Close workspace search" : "Search workspace"}
					aria-label={searching ? "Close workspace search" : "Search workspace"}
					className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-fg-dim hover:bg-bg-hover hover:text-fg"
				>
					{searching ? <X size={12} /> : <Search size={12} />}
				</button>
			</div>
			<div className={searching ? "hidden" : "flex min-h-0 flex-1 flex-col"}>
				<FileTree
					onFileSelect={onFileSelect}
					onFileMove={onFileMove}
					platform={platform}
				/>
			</div>
			<div className={searching ? "min-h-0 flex-1" : "hidden"}>
				<SearchPanel
					active={searching}
					focusKey={searchFocusKey}
					onClose={onCloseSearch}
					onOpenFile={onOpenSearchResult}
					onApplyWorkspaceEdit={onApplyWorkspaceEdit}
					subscribe={subscribe}
				/>
			</div>
		</div>
	);
}
