import {
	ChevronDown,
	ChevronRight,
	ClipboardCopy,
	ClipboardPaste,
	Copy,
	File,
	FilePlus,
	FileText,
	Folder,
	FolderOpen,
	FolderPlus,
	FolderSearch,
	Pencil,
	Scissors,
	Trash2,
} from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { createWorkspaceFile } from "../api/files";
import type { FileEntry, ServerMessage } from "../types/protocol";
import { getDeviconClass } from "../utils/fileIcons";
import { Dialog, dialogButtonClass, useToast } from "./ui/Feedback";
import { FloatingMenu } from "./ui/Floating";

interface Props {
	onFileSelect: (path: string) => void;
	onFileMove?: (from: string, to: string) => void;
	platform?: string;
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
	node: TreeNode | null;
}

interface CreateState {
	parent: string;
	directory: boolean;
}

interface FileClipboard {
	path: string;
	directory: boolean;
	operation: "copy" | "cut";
}

export function FileTree({
	onFileSelect,
	onFileMove,
	platform,
	subscribe,
}: Props) {
	const toast = useToast();
	const [nodes, setNodes] = useState<TreeNode[]>([]);
	const [menu, setMenu] = useState<MenuState | null>(null);
	const [renaming, setRenaming] = useState<string | null>(null);
	const [renameValue, setRenameValue] = useState("");
	const [creating, setCreating] = useState<CreateState | null>(null);
	const [createValue, setCreateValue] = useState("");
	const [fileClipboard, setFileClipboard] = useState<FileClipboard | null>(
		null,
	);
	const [focusedPath, setFocusedPath] = useState<string | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<TreeNode | null>(null);
	const nodesRef = useRef(nodes);
	const refreshRef = useRef(0);
	const renameSubmittingRef = useRef(false);
	const renameCanceledRef = useRef(false);
	const createSubmittingRef = useRef(false);
	const createCanceledRef = useRef(false);
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

	const refresh = useCallback(async () => {
		const request = ++refreshRef.current;
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
			const next = await refreshLevel("", nodesRef.current);
			if (refreshRef.current === request) setNodes(next);
		} catch (error) {
			if (refreshRef.current !== request) return;
			toast({
				title: "Could not refresh files",
				description: String(error),
				tone: "error",
			});
		}
	}, [loadDir, toast]);

	useEffect(() => {
		void refresh();
	}, [refresh]);

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
			refreshRef.current++;
			const target = findNode(nodesRef.current, path);
			if (!target?.is_dir) return;
			if (target.loaded) {
				setNodes((current) =>
					updateNode(current, path, (node) => ({
						...node,
						expanded: !node.expanded,
					})),
				);
				return;
			}

			const children = await loadDir(path);
			setNodes((current) =>
				updateNode(current, path, (node) =>
					node.loaded
						? { ...node, expanded: true }
						: { ...node, expanded: true, loaded: true, children },
				),
			);
		},
		[loadDir],
	);

	const beginRename = (node: TreeNode) => {
		setMenu(null);
		requestAnimationFrame(() => {
			renameCanceledRef.current = false;
			setRenaming(node.path);
			setRenameValue(node.name);
		});
	};

	const commitRename = async (node: TreeNode) => {
		if (renameSubmittingRef.current) return;
		if (renameCanceledRef.current) {
			renameCanceledRef.current = false;
			return;
		}
		const newName = renameValue.trim();
		setRenaming(null);
		if (!newName || newName === node.name) return;
		if (newName.includes("/") || newName.includes("\\")) return;

		const parent = node.path.includes("/")
			? node.path.slice(0, node.path.lastIndexOf("/"))
			: "";
		const to = parent ? `${parent}/${newName}` : newName;

		renameSubmittingRef.current = true;
		try {
			const res = await fetch("/api/files/rename", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ from: node.path, to }),
			});
			if (res.ok) {
				setFileClipboard((current) =>
					current
						? { ...current, path: movePath(current.path, node.path, to) }
						: current,
				);
				onFileMove?.(node.path, to);
				return;
			}
			toast({
				title: "Rename failed",
				description: await res.text(),
				tone: "error",
			});
		} catch (error) {
			toast({
				title: "Rename failed",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		} finally {
			renameSubmittingRef.current = false;
		}
	};

	const beginCreate = async (node: TreeNode | null, directory: boolean) => {
		setMenu(null);
		const parent = containingDirectory(node);

		try {
			if (node?.is_dir && !node.expanded) {
				await toggleDir(node.path);
			}
		} catch (error) {
			toast({
				title: "Could not load folder",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
			return;
		}

		requestAnimationFrame(() => {
			setCreateValue("");
			createCanceledRef.current = false;
			setCreating({ parent, directory });
		});
	};

	const commitCreate = async (target: CreateState) => {
		if (createSubmittingRef.current) return;
		if (createCanceledRef.current) {
			createCanceledRef.current = false;
			return;
		}
		const name = createValue.trim();
		if (!name) {
			setCreating(null);
			return;
		}
		if (
			name === "." ||
			name === ".." ||
			name.includes("/") ||
			name.includes("\\")
		) {
			toast({
				title: `Invalid ${target.directory ? "folder" : "file"} name`,
				description: "Enter a name without path separators.",
				tone: "error",
			});
			return;
		}

		const filePath = target.parent ? `${target.parent}/${name}` : name;
		createSubmittingRef.current = true;
		try {
			await createWorkspaceFile(filePath, { directory: target.directory });
			setCreating(null);
			await refresh();
			if (!target.directory) onFileSelect(filePath);
		} catch (error) {
			toast({
				title: `Could not create ${target.directory ? "folder" : "file"}`,
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		} finally {
			createSubmittingRef.current = false;
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
		setFileClipboard((current) =>
			current && isSameOrChild(current.path, node.path) ? null : current,
		);
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

	const handleCopyPath = async (node: TreeNode, relative: boolean) => {
		setMenu(null);
		try {
			let value = node.path;
			if (!relative) {
				const res = await fetch(
					`/api/files/path?path=${encodeURIComponent(node.path)}`,
				);
				if (!res.ok) throw new Error(await res.text());
				value = ((await res.json()) as { path: string }).path;
			}
			await navigator.clipboard.writeText(value);
		} catch (error) {
			toast({
				title: "Copy path failed",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
	};

	const handleReveal = async (node: TreeNode) => {
		setMenu(null);
		try {
			const res = await fetch("/api/files/reveal", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ path: node.path }),
			});
			if (!res.ok) throw new Error(await res.text());
		} catch (error) {
			toast({
				title: "Could not reveal path",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
	};

	const handleCut = (node: TreeNode) => {
		setMenu(null);
		setFileClipboard({
			path: node.path,
			directory: node.is_dir,
			operation: "cut",
		});
	};

	const handleCopy = (node: TreeNode) => {
		setMenu(null);
		setFileClipboard({
			path: node.path,
			directory: node.is_dir,
			operation: "copy",
		});
	};

	const handlePaste = async (node: TreeNode | null) => {
		setMenu(null);
		const clipboard = fileClipboard;
		if (!clipboard) return;

		const parent = containingDirectory(node);
		const sourcePath = clipboard.path;
		const sourceName = baseName(sourcePath);
		const initialTo = parent ? `${parent}/${sourceName}` : sourceName;
		if (clipboard.operation === "cut" && initialTo === sourcePath) {
			setFileClipboard(null);
			return;
		}
		if (clipboard.directory && isSameOrChild(parent, sourcePath)) {
			toast({
				title: "Paste failed",
				description: `A folder cannot be ${clipboard.operation === "cut" ? "moved" : "copied"} into itself.`,
				tone: "error",
			});
			return;
		}

		try {
			let destination = initialTo;
			for (let attempt = 0; attempt <= 50; attempt++) {
				if (clipboard.operation === "copy" && attempt > 0) {
					const name = copiedName(sourceName, clipboard.directory, attempt);
					destination = parent ? `${parent}/${name}` : name;
				}
				const res = await fetch(
					clipboard.operation === "cut"
						? "/api/files/rename"
						: "/api/files/copy",
					{
						method: "POST",
						headers: { "Content-Type": "application/json" },
						body: JSON.stringify({ from: sourcePath, to: destination }),
					},
				);
				if (res.ok) break;
				if (
					clipboard.operation === "copy" &&
					res.status === 409 &&
					attempt < 50
				) {
					continue;
				}
				throw new Error((await res.text()).trim() || "File operation failed.");
			}
			if (clipboard.operation === "cut") {
				onFileMove?.(sourcePath, destination);
				setFileClipboard(null);
			}
			await refresh();
		} catch (error) {
			toast({
				title: "Paste failed",
				description: error instanceof Error ? error.message : String(error),
				tone: "error",
			});
		}
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

	const renderCreateInput = (target: CreateState, depth: number) => (
		<div
			className="flex items-center gap-1 py-[3px] pr-2 text-[12px] leading-snug"
			style={{ paddingLeft: 8 + depth * 12 }}
			role="treeitem"
			aria-level={depth + 1}
		>
			<span className="w-3.5 shrink-0" />
			<span className="flex h-3.5 w-3.5 shrink-0 items-center justify-center text-fg-dim">
				{target.directory ? <Folder size={14} /> : <File size={13} />}
			</span>
			<input
				autoFocus
				value={createValue}
				onChange={(event) => setCreateValue(event.target.value)}
				onKeyDown={(event) => {
					if (event.key === "Enter") void commitCreate(target);
					else if (event.key === "Escape") {
						createCanceledRef.current = true;
						setCreating(null);
					}
				}}
				onBlur={() => void commitCreate(target)}
				aria-label={`New ${target.directory ? "folder" : "file"} name${target.parent ? ` in ${target.parent}` : " in workspace"}`}
				className="ml-0.5 min-w-0 flex-1 rounded border border-border-strong bg-bg-surface px-1 py-0 text-[12px] text-fg outline-none"
			/>
		</div>
	);

	const renderNodes = (items: TreeNode[], depth: number) => {
		return items.map((node, index) => {
			const isRenaming = renaming === node.path;
			return (
				<div key={node.path}>
					<div
						className={`flex items-center gap-1 py-[3px] pr-2 cursor-pointer text-fg-muted whitespace-nowrap text-[12px] leading-snug select-none hover:bg-bg-hover hover:text-fg transition-colors ${fileClipboard?.operation === "cut" && fileClipboard.path === node.path ? "opacity-50" : ""}`}
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
									else if (e.key === "Escape") {
										renameCanceledRef.current = true;
										setRenaming(null);
									}
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
						<div role="group">
							{creating?.parent === node.path &&
								renderCreateInput(creating, depth + 1)}
							{renderNodes(node.children, depth + 1)}
						</div>
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
			onContextMenu={(event) => {
				if ((event.target as Element).closest("[data-tree-path]")) return;
				event.preventDefault();
				setMenu({ x: event.clientX, y: event.clientY, node: null });
			}}
		>
			{creating?.parent === "" && renderCreateInput(creating, 0)}
			{renderNodes(nodes, 0)}
			{menu && (
				<ContextMenu
					menu={menu}
					onClose={() => setMenu(null)}
					onOpen={() => {
						setMenu(null);
						if (menu.node) onFileSelect(menu.node.path);
					}}
					onNewFile={() => void beginCreate(menu.node, false)}
					onNewFolder={() => void beginCreate(menu.node, true)}
					onRename={() => menu.node && beginRename(menu.node)}
					onDuplicate={() => menu.node && handleDuplicate(menu.node)}
					onCopyPath={() => menu.node && void handleCopyPath(menu.node, false)}
					onCopyRelativePath={() =>
						menu.node && void handleCopyPath(menu.node, true)
					}
					onReveal={() => menu.node && void handleReveal(menu.node)}
					platform={platform}
					onCut={() => menu.node && handleCut(menu.node)}
					onCopy={() => menu.node && handleCopy(menu.node)}
					onPaste={() => void handlePaste(menu.node)}
					canPaste={fileClipboard !== null}
					onDelete={() => menu.node && requestDelete(menu.node)}
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

function findNode(nodes: TreeNode[], path: string): TreeNode | undefined {
	for (const node of nodes) {
		if (node.path === path) return node;
		const child = node.children && findNode(node.children, path);
		if (child) return child;
	}
}

function updateNode(
	nodes: TreeNode[],
	path: string,
	update: (node: TreeNode) => TreeNode,
): TreeNode[] {
	return nodes.map((node) => {
		if (node.path === path) return update(node);
		return node.children
			? { ...node, children: updateNode(node.children, path, update) }
			: node;
	});
}

function containingDirectory(node: TreeNode | null): string {
	if (node?.is_dir) return node.path;
	if (!node) return "";
	const slash = node.path.lastIndexOf("/");
	return slash < 0 ? "" : node.path.slice(0, slash);
}

function baseName(path: string): string {
	return path.slice(path.lastIndexOf("/") + 1);
}

function isSameOrChild(path: string, parent: string): boolean {
	return path === parent || path.startsWith(`${parent}/`);
}

function movePath(path: string, from: string, to: string): string {
	return isSameOrChild(path, from) ? `${to}${path.slice(from.length)}` : path;
}

function copiedName(name: string, directory: boolean, attempt: number): string {
	const dot = directory ? -1 : name.lastIndexOf(".");
	const stem = dot > 0 ? name.slice(0, dot) : name;
	const extension = dot > 0 ? name.slice(dot) : "";
	const suffix = attempt === 1 ? " copy" : ` copy ${attempt}`;
	return `${stem}${suffix}${extension}`;
}

function revealLabel(platform?: string): string {
	const value = (platform || navigator.platform).toLowerCase();
	if (value.includes("darwin") || value.includes("mac")) {
		return "Reveal in Finder";
	}
	if (value.includes("win")) return "Reveal in Explorer";
	return "Reveal in File Manager";
}

interface ContextMenuProps {
	menu: MenuState;
	onClose: () => void;
	onOpen: () => void;
	onNewFile: () => void;
	onNewFolder: () => void;
	onRename: () => void;
	onDuplicate: () => void;
	onCopyPath: () => void;
	onCopyRelativePath: () => void;
	onReveal: () => void;
	platform?: string;
	onCut: () => void;
	onCopy: () => void;
	onPaste: () => void;
	canPaste: boolean;
	onDelete: () => void;
}

function ContextMenu({
	menu,
	onClose,
	onOpen,
	onNewFile,
	onNewFolder,
	onRename,
	onDuplicate,
	onCopyPath,
	onCopyRelativePath,
	onReveal,
	platform,
	onCut,
	onCopy,
	onPaste,
	canPaste,
	onDelete,
}: ContextMenuProps) {
	const isDir = menu.node?.is_dir ?? true;
	return (
		<FloatingMenu
			open
			onOpenChange={(open) => !open && onClose()}
			reference={{ x: menu.x, y: menu.y }}
			label={menu.node ? `Actions for ${menu.node.name}` : "Workspace actions"}
			className="z-[100] min-w-[160px] bg-bg-elevated border border-border-subtle rounded-md shadow-2xl py-1 text-[12px]"
		>
			{menu.node && !isDir && (
				<MenuItem icon={<FileText size={12} />} label="Open" onClick={onOpen} />
			)}
			<MenuItem
				icon={<FilePlus size={12} />}
				label="New File…"
				onClick={onNewFile}
			/>
			<MenuItem
				icon={<FolderPlus size={12} />}
				label="New Folder…"
				onClick={onNewFolder}
			/>
			<div role="separator" className="my-1 border-t border-border-subtle" />
			{menu.node && (
				<MenuItem icon={<Scissors size={12} />} label="Cut" onClick={onCut} />
			)}
			{menu.node && (
				<MenuItem
					icon={<ClipboardCopy size={12} />}
					label="Copy"
					onClick={onCopy}
				/>
			)}
			<MenuItem
				icon={<ClipboardPaste size={12} />}
				label="Paste"
				onClick={onPaste}
				disabled={!canPaste}
			/>
			{menu.node && (
				<MenuItem
					icon={<Copy size={12} />}
					label="Duplicate"
					onClick={onDuplicate}
				/>
			)}
			{menu.node && (
				<MenuItem
					icon={<Copy size={12} />}
					label="Copy Path"
					onClick={onCopyPath}
				/>
			)}
			{menu.node && (
				<MenuItem
					icon={<Copy size={12} />}
					label="Copy Relative Path"
					onClick={onCopyRelativePath}
				/>
			)}
			{menu.node && (
				<MenuItem
					icon={<FolderSearch size={12} />}
					label={revealLabel(platform)}
					onClick={onReveal}
				/>
			)}
			{menu.node && (
				<MenuItem
					icon={<Pencil size={12} />}
					label="Rename"
					onClick={onRename}
				/>
			)}
			{menu.node && (
				<>
					<div
						role="separator"
						className="my-1 border-t border-border-subtle"
					/>
					<MenuItem
						icon={<Trash2 size={12} />}
						label="Delete"
						onClick={onDelete}
						danger
					/>
				</>
			)}
		</FloatingMenu>
	);
}

function MenuItem({
	icon,
	label,
	onClick,
	danger,
	disabled,
}: {
	icon: React.ReactNode;
	label: string;
	onClick: () => void;
	danger?: boolean;
	disabled?: boolean;
}) {
	return (
		<button
			type="button"
			role="menuitem"
			disabled={disabled}
			className={`w-full flex items-center gap-2 px-3 py-1.5 text-left transition-colors disabled:cursor-default disabled:opacity-40 ${disabled ? "" : "hover:bg-bg-hover"} ${danger ? "text-danger" : "text-fg-muted enabled:hover:text-fg"}`}
			onClick={onClick}
		>
			<span className="w-3.5 flex items-center justify-center shrink-0">
				{icon}
			</span>
			<span>{label}</span>
		</button>
	);
}
