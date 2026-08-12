import {
	ChevronDown,
	ChevronRight,
	ClipboardCopy,
	Copy,
	Download,
	File,
	FileText,
	Folder,
	FolderOpen,
	Pencil,
	Trash2,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import type { FileEntry, ServerMessage } from "../types/protocol";
import { getDeviconClass } from "../utils/fileIcons";
import { Dialog, dialogButtonClass, useToast } from "./ui/Feedback";
import { FloatingMenu } from "./ui/Floating";

interface Props {
	onFileSelect: (path: string) => void;
	subscribe?: (handler: (msg: ServerMessage) => void) => () => void;
}

interface TreeNode extends FileEntry {
	children?: TreeNode[];
	expanded?: boolean;
	loaded?: boolean;
}

interface MenuState {
	x: number;
	y: number;
	node: TreeNode;
}

export function FileTree({ onFileSelect, subscribe }: Props) {
	const toast = useToast();
	const [nodes, setNodes] = useState<TreeNode[]>([]);
	const [menu, setMenu] = useState<MenuState | null>(null);
	const [renaming, setRenaming] = useState<string | null>(null);
	const [renameValue, setRenameValue] = useState("");
	const [focusedPath, setFocusedPath] = useState<string | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<TreeNode | null>(null);
	const nodesRef = useRef(nodes);
	useEffect(() => {
		nodesRef.current = nodes;
	});

	const loadDir = useCallback(async (dirPath: string): Promise<TreeNode[]> => {
		const res = await fetch(
			`/api/files?path=${encodeURIComponent(dirPath || "")}`,
		);
		if (!res.ok)
			throw new Error((await res.text()).trim() || "Failed to load files.");
		const files: FileEntry[] = await res.json();

		return files
			.sort((a, b) => {
				if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;
				return a.name.localeCompare(b.name);
			})
			.map((f) => ({ ...f, expanded: false, loaded: false }));
	}, []);

	useEffect(() => {
		loadDir("")
			.then(setNodes)
			.catch((error) => {
				toast({
					title: "Could not load files",
					description: String(error),
					tone: "error",
				});
			});
	}, [loadDir, toast]);

	const refresh = useCallback(async () => {
		const refreshLevel = async (
			path: string,
			prev: TreeNode[],
		): Promise<TreeNode[]> => {
			const fresh = await loadDir(path);
			const prevByPath = new Map(prev.map((n) => [n.path, n]));
			const result: TreeNode[] = [];
			for (const f of fresh) {
				const old = prevByPath.get(f.path);
				if (old?.is_dir && old.expanded && old.loaded && old.children) {
					const newChildren = await refreshLevel(f.path, old.children);
					result.push({
						...f,
						expanded: true,
						loaded: true,
						children: newChildren,
					});
				} else {
					result.push({ ...f, expanded: false, loaded: false });
				}
			}
			return result;
		};
		try {
			setNodes(await refreshLevel("", nodesRef.current));
		} catch (error) {
			toast({
				title: "Could not refresh files",
				description: String(error),
				tone: "error",
			});
		}
	}, [loadDir, toast]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((msg) => {
			if (msg.type === "files_changed") {
				refresh();
			}
		});
	}, [subscribe, refresh]);

	const toggleDir = useCallback(
		async (path: string) => {
			const toggle = async (items: TreeNode[]): Promise<TreeNode[]> => {
				const result: TreeNode[] = [];
				for (const node of items) {
					if (node.path === path && node.is_dir) {
						if (!node.loaded) {
							const children = await loadDir(node.path);
							result.push({ ...node, expanded: true, loaded: true, children });
						} else {
							result.push({ ...node, expanded: !node.expanded });
						}
					} else if (node.children) {
						result.push({ ...node, children: await toggle(node.children) });
					} else {
						result.push(node);
					}
				}
				return result;
			};
			setNodes(await toggle(nodes));
		},
		[nodes, loadDir],
	);

	const beginRename = (node: TreeNode) => {
		setRenaming(node.path);
		setRenameValue(node.name);
		setMenu(null);
	};

	const commitRename = async (node: TreeNode) => {
		const newName = renameValue.trim();
		setRenaming(null);
		if (!newName || newName === node.name) return;
		if (newName.includes("/") || newName.includes("\\")) return;

		const parent = node.path.includes("/")
			? node.path.slice(0, node.path.lastIndexOf("/"))
			: "";
		const to = parent ? `${parent}/${newName}` : newName;

		const res = await fetch("/api/files/rename", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ from: node.path, to }),
		});
		if (!res.ok) {
			toast({
				title: "Rename failed",
				description: await res.text(),
				tone: "error",
			});
		}
	};

	const requestDelete = (node: TreeNode) => {
		setMenu(null);
		setDeleteTarget(node);
	};

	const confirmDelete = async () => {
		const node = deleteTarget;
		if (!node) return;
		const res = await fetch(
			`/api/files?path=${encodeURIComponent(node.path)}`,
			{ method: "DELETE" },
		);
		if (!res.ok) {
			toast({
				title: "Delete failed",
				description: await res.text(),
				tone: "error",
			});
			return;
		}
		setDeleteTarget(null);
	};

	const handleDuplicate = async (node: TreeNode) => {
		setMenu(null);
		const dot = node.is_dir ? -1 : node.name.lastIndexOf(".");
		const stem = dot > 0 ? node.name.slice(0, dot) : node.name;
		const ext = dot > 0 ? node.name.slice(dot) : "";
		const parent = node.path.includes("/")
			? node.path.slice(0, node.path.lastIndexOf("/"))
			: "";

		for (let i = 1; i <= 50; i++) {
			const suffix = i === 1 ? " copy" : ` copy ${i}`;
			const candidate = `${stem}${suffix}${ext}`;
			const to = parent ? `${parent}/${candidate}` : candidate;
			const res = await fetch("/api/files/copy", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ from: node.path, to }),
			});
			if (res.ok) return;
			if (res.status !== 409) {
				toast({
					title: "Duplicate failed",
					description: await res.text(),
					tone: "error",
				});
				return;
			}
		}
		toast({
			title: "Duplicate failed",
			description: "Too many copies already exist.",
			tone: "error",
		});
	};

	const handleCopy = async (node: TreeNode) => {
		setMenu(null);
		try {
			await navigator.clipboard.write([
				new ClipboardItem({
					"text/plain": fetch(
						`/api/files/read?path=${encodeURIComponent(node.path)}`,
					).then(async (res) => {
						if (!res.ok) throw new Error(await res.text());
						const data = (await res.json()) as { content: string };
						return new Blob([data.content], { type: "text/plain" });
					}),
				}),
			]);
		} catch (e) {
			toast({
				title: "Copy failed",
				description: e instanceof Error ? e.message : String(e),
				tone: "error",
			});
		}
	};

	const handleDownload = (node: TreeNode) => {
		setMenu(null);
		const a = document.createElement("a");
		a.href = `/api/files/download?path=${encodeURIComponent(node.path)}`;
		a.download = node.name;
		document.body.appendChild(a);
		a.click();
		document.body.removeChild(a);
	};

	const focusTreePath = (path: string) => {
		setFocusedPath(path);
		requestAnimationFrame(() =>
			document
				.querySelector<HTMLElement>(`[data-tree-path="${CSS.escape(path)}"]`)
				?.focus(),
		);
	};

	const handleTreeKey = (event: React.KeyboardEvent, node: TreeNode) => {
		const visible = flattenVisible(nodes);
		const index = visible.findIndex((item) => item.node.path === node.path);
		if (event.key === "ArrowDown" || event.key === "ArrowUp") {
			event.preventDefault();
			const offset = event.key === "ArrowDown" ? 1 : -1;
			const next =
				visible[Math.max(0, Math.min(visible.length - 1, index + offset))];
			if (next) focusTreePath(next.node.path);
			return;
		}
		if (event.key === "ArrowRight" && node.is_dir) {
			event.preventDefault();
			if (!node.expanded) void toggleDir(node.path);
			else if (node.children?.[0]) focusTreePath(node.children[0].path);
			return;
		}
		if (event.key === "ArrowLeft") {
			event.preventDefault();
			if (node.is_dir && node.expanded) void toggleDir(node.path);
			else {
				const current = visible[index];
				const parent = [...visible.slice(0, index)]
					.reverse()
					.find((item) => item.depth === current.depth - 1);
				if (parent) focusTreePath(parent.node.path);
			}
			return;
		}
		if (event.key === "Enter" || event.key === " ") {
			event.preventDefault();
			if (node.is_dir) void toggleDir(node.path);
			else onFileSelect(node.path);
			return;
		}
		if (
			event.key === "ContextMenu" ||
			(event.shiftKey && event.key === "F10")
		) {
			event.preventDefault();
			const rect = event.currentTarget.getBoundingClientRect();
			setMenu({ x: rect.left + 24, y: rect.bottom, node });
		}
	};

	const renderNodes = (items: TreeNode[], depth: number) => {
		return items.map((node, index) => {
			const isRenaming = renaming === node.path;
			return (
				<div key={node.path}>
					<div
						className="flex items-center gap-1 py-[3px] pr-2 cursor-pointer text-fg-muted whitespace-nowrap text-[12px] leading-snug select-none hover:bg-bg-hover hover:text-fg transition-colors"
						style={{ paddingLeft: 8 + depth * 12 }}
						draggable={!node.is_dir && !isRenaming}
						onDragStart={(e) => {
							e.dataTransfer.setData("application/x-wingman-file", node.path);
							e.dataTransfer.setData("text/plain", node.path);
							e.dataTransfer.effectAllowed = "copy";
						}}
						onClick={() => {
							if (isRenaming) return;
							if (node.is_dir) {
								toggleDir(node.path);
							} else {
								onFileSelect(node.path);
							}
						}}
						onContextMenu={(e) => {
							e.preventDefault();
							e.stopPropagation();
							setMenu({ x: e.clientX, y: e.clientY, node });
						}}
						title={node.name}
						role="treeitem"
						aria-level={depth + 1}
						aria-expanded={node.is_dir ? !!node.expanded : undefined}
						tabIndex={
							focusedPath === node.path ||
							(!focusedPath && depth === 0 && index === 0)
								? 0
								: -1
						}
						data-tree-path={node.path}
						onFocus={() => setFocusedPath(node.path)}
						onKeyDown={(event) => handleTreeKey(event, node)}
					>
						<span className="w-3.5 flex items-center justify-center shrink-0 text-fg-dim">
							{node.is_dir ? (
								node.expanded ? (
									<ChevronDown size={12} />
								) : (
									<ChevronRight size={12} />
								)
							) : null}
						</span>
						<span
							className={`shrink-0 flex items-center justify-center w-3.5 h-3.5 ${node.is_dir ? "text-fg-muted" : "text-fg-dim"}`}
						>
							{node.is_dir ? (
								node.expanded ? (
									<FolderOpen size={14} />
								) : (
									<Folder size={14} />
								)
							) : (
								(() => {
									const cls = getDeviconClass(node.name);
									if (cls) {
										return <i className={`${cls} text-[14px] leading-none`} />;
									}
									return <File size={13} />;
								})()
							)}
						</span>
						{isRenaming ? (
							<input
								autoFocus
								value={renameValue}
								onChange={(e) => setRenameValue(e.target.value)}
								onClick={(e) => e.stopPropagation()}
								onKeyDown={(e) => {
									if (e.key === "Enter") commitRename(node);
									else if (e.key === "Escape") setRenaming(null);
								}}
								onBlur={() => commitRename(node)}
								className="ml-0.5 bg-bg-surface border border-border-strong rounded px-1 py-0 text-fg text-[12px] outline-none min-w-0 flex-1"
							/>
						) : (
							<span
								className={`overflow-hidden text-ellipsis ml-0.5 ${node.is_dir ? "text-fg" : ""}`}
							>
								{node.name}
							</span>
						)}
					</div>
					{node.expanded && node.children && (
						<div role="group">{renderNodes(node.children, depth + 1)}</div>
					)}
				</div>
			);
		});
	};

	return (
		<div
			className="relative flex-1 overflow-y-auto bg-transparent py-2"
			role="tree"
			aria-label="Workspace files"
		>
			{renderNodes(nodes, 0)}
			{menu && (
				<ContextMenu
					menu={menu}
					onClose={() => setMenu(null)}
					onOpen={() => {
						setMenu(null);
						onFileSelect(menu.node.path);
					}}
					onRename={() => beginRename(menu.node)}
					onDuplicate={() => handleDuplicate(menu.node)}
					onCopy={() => handleCopy(menu.node)}
					onDownload={() => handleDownload(menu.node)}
					onDelete={() => requestDelete(menu.node)}
				/>
			)}
			<Dialog
				open={deleteTarget !== null}
				title={`Delete ${deleteTarget?.is_dir ? "folder" : "file"}?`}
				description={
					deleteTarget
						? `“${deleteTarget.name}” will be permanently deleted.`
						: undefined
				}
				onClose={() => setDeleteTarget(null)}
			>
				<button
					type="button"
					className={dialogButtonClass}
					onClick={() => setDeleteTarget(null)}
				>
					Cancel
				</button>
				<button
					type="button"
					className={`${dialogButtonClass} border-danger/40 text-danger hover:bg-danger/10`}
					onClick={() => void confirmDelete()}
				>
					Delete
				</button>
			</Dialog>
		</div>
	);
}

function flattenVisible(
	nodes: TreeNode[],
	depth = 0,
): Array<{ node: TreeNode; depth: number }> {
	const result: Array<{ node: TreeNode; depth: number }> = [];
	for (const node of nodes) {
		result.push({ node, depth });
		if (node.is_dir && node.expanded && node.children) {
			result.push(...flattenVisible(node.children, depth + 1));
		}
	}
	return result;
}

interface ContextMenuProps {
	menu: MenuState;
	onClose: () => void;
	onOpen: () => void;
	onRename: () => void;
	onDuplicate: () => void;
	onCopy: () => void;
	onDownload: () => void;
	onDelete: () => void;
}

function ContextMenu({
	menu,
	onClose,
	onOpen,
	onRename,
	onDuplicate,
	onCopy,
	onDownload,
	onDelete,
}: ContextMenuProps) {
	const isDir = menu.node.is_dir;
	return (
		<FloatingMenu
			open
			onOpenChange={(open) => !open && onClose()}
			reference={{ x: menu.x, y: menu.y }}
			label={`Actions for ${menu.node.name}`}
			className="z-[100] min-w-[160px] bg-bg-elevated border border-border-subtle rounded-md shadow-2xl py-1 text-[12px]"
		>
			{!isDir && (
				<MenuItem icon={<FileText size={12} />} label="Open" onClick={onOpen} />
			)}
			{!isDir && (
				<MenuItem
					icon={<ClipboardCopy size={12} />}
					label="Copy"
					onClick={onCopy}
				/>
			)}
			<MenuItem icon={<Pencil size={12} />} label="Rename" onClick={onRename} />
			<MenuItem
				icon={<Copy size={12} />}
				label="Duplicate"
				onClick={onDuplicate}
			/>
			{!isDir && (
				<MenuItem
					icon={<Download size={12} />}
					label="Download"
					onClick={onDownload}
				/>
			)}
			<div role="separator" className="my-1 border-t border-border-subtle" />
			<MenuItem
				icon={<Trash2 size={12} />}
				label="Delete"
				onClick={onDelete}
				danger
			/>
		</FloatingMenu>
	);
}

function MenuItem({
	icon,
	label,
	onClick,
	danger,
}: {
	icon: React.ReactNode;
	label: string;
	onClick: () => void;
	danger?: boolean;
}) {
	return (
		<button
			type="button"
			role="menuitem"
			className={`w-full flex items-center gap-2 px-3 py-1.5 text-left hover:bg-bg-hover transition-colors ${danger ? "text-danger" : "text-fg-muted hover:text-fg"}`}
			onClick={onClick}
		>
			<span className="w-3.5 flex items-center justify-center shrink-0">
				{icon}
			</span>
			<span>{label}</span>
		</button>
	);
}
