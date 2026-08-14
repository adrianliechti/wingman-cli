import {
	AlignLeft,
	Braces,
	ClipboardPaste,
	Copy,
	FileCode2,
	GitFork,
	Lightbulb,
	PanelTopOpen,
	PencilLine,
	Redo2,
	ScanText,
	Scissors,
	Search,
	Undo2,
} from "lucide-react";
import type * as MonacoTypes from "monaco-editor";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import type { MonacoLanguageFeature } from "../monacoLsp";
import { FloatingMenu } from "./ui/Floating";

interface Point {
	x: number;
	y: number;
}

interface Props {
	editor: MonacoTypes.editor.IStandaloneCodeEditor;
	openAt: Point;
	readOnly: boolean;
	initialAltKey?: boolean;
	supportsLanguageFeature: (feature: MonacoLanguageFeature) => boolean;
	onClose: () => void;
}

interface MenuItem {
	key: string;
	label: string;
	icon: ReactNode;
	shortcut?: string;
	enabled: boolean;
	run: () => void | Promise<void>;
}

interface ActionDefinition {
	id: string;
	label: string;
	icon: ReactNode;
	shortcut?: string;
}

interface LanguageActionDefinition extends ActionDefinition {
	feature: MonacoLanguageFeature;
	alternate?: ActionDefinition;
}

const navigationActions: LanguageActionDefinition[] = [
	{
		id: "editor.action.revealDefinition",
		feature: "definition",
		label: "Go to Definition",
		icon: <FileCode2 size={13} />,
		shortcut: "F12",
		alternate: {
			id: "editor.action.peekDefinition",
			label: "Peek Definition",
			icon: <PanelTopOpen size={13} />,
			shortcut: "⌥F12",
		},
	},
	{
		id: "editor.action.goToTypeDefinition",
		feature: "typeDefinition",
		label: "Go to Type Definition",
		icon: <Braces size={13} />,
		alternate: {
			id: "editor.action.peekTypeDefinition",
			label: "Peek Type Definition",
			icon: <PanelTopOpen size={13} />,
		},
	},
	{
		id: "editor.action.goToImplementation",
		feature: "implementation",
		label: "Go to Implementations",
		icon: <GitFork size={13} />,
		alternate: {
			id: "editor.action.peekImplementation",
			label: "Peek Implementations",
			icon: <PanelTopOpen size={13} />,
		},
	},
	{
		id: "editor.action.referenceSearch.trigger",
		feature: "references",
		label: "Find All References",
		icon: <Search size={13} />,
		shortcut: "⇧F12",
	},
];

const codeActions: ActionDefinition[] = [
	{
		id: "editor.action.quickFix",
		label: "Quick Fix…",
		icon: <Lightbulb size={13} />,
	},
	{
		id: "editor.action.rename",
		label: "Rename Symbol…",
		icon: <PencilLine size={13} />,
		shortcut: "F2",
	},
	{
		id: "editor.action.changeAll",
		label: "Change All Occurrences",
		icon: <ScanText size={13} />,
	},
];

export function EditorContextMenu({
	editor,
	openAt,
	readOnly,
	initialAltKey = false,
	supportsLanguageFeature,
	onClose,
}: Props) {
	const [altKey, setAltKey] = useState(initialAltKey);
	useEffect(() => {
		setAltKey(initialAltKey);
	}, [initialAltKey, openAt]);
	useEffect(() => {
		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.altKey) setAltKey(true);
		};
		const handleKeyUp = (event: KeyboardEvent) => {
			if (event.key === "Alt" || !event.altKey) setAltKey(false);
		};
		const handleBlur = () => setAltKey(false);
		window.addEventListener("keydown", handleKeyDown, true);
		window.addEventListener("keyup", handleKeyUp, true);
		window.addEventListener("blur", handleBlur);
		return () => {
			window.removeEventListener("keydown", handleKeyDown, true);
			window.removeEventListener("keyup", handleKeyUp, true);
			window.removeEventListener("blur", handleBlur);
		};
	}, []);

	const mac = /Mac|iPhone|iPad/.test(navigator.platform);
	const primary = mac ? "⌘" : "Ctrl+";
	const model = editor.getModel();
	const editItems: MenuItem[] = [
		commandItem(editor, "undo", "Undo", <Undo2 size={13} />, `${primary}Z`, {
			enabled: model?.canUndo() === true,
		}),
		commandItem(
			editor,
			"redo",
			"Redo",
			<Redo2 size={13} />,
			mac ? "⇧⌘Z" : "Ctrl+Y",
			{ enabled: model?.canRedo() === true },
		),
		commandItem(
			editor,
			"editor.action.clipboardCutAction",
			"Cut",
			<Scissors size={13} />,
			`${primary}X`,
			{ enabled: !readOnly },
		),
		commandItem(
			editor,
			"editor.action.clipboardCopyAction",
			"Copy",
			<Copy size={13} />,
			`${primary}C`,
		),
		commandItem(
			editor,
			"editor.action.clipboardPasteAction",
			"Paste",
			<ClipboardPaste size={13} />,
			`${primary}V`,
			{ enabled: !readOnly },
		),
	];
	const navigationItems = languageActions(
		editor,
		navigationActions,
		supportsLanguageFeature,
		altKey,
	);
	const supportedCodeActions = useMemo(
		() => (readOnly ? [] : supportedActions(editor, codeActions)),
		[editor, readOnly],
	);
	const selection = editor.getSelection();
	const hasSelection = !!selection && !selection.isEmpty();
	const formatItems = readOnly
		? []
		: supportedActions(editor, [
				{
					id: hasSelection
						? "editor.action.formatSelection"
						: "editor.action.formatDocument",
					label: hasSelection ? "Format Selection" : "Format Document",
					icon: <AlignLeft size={13} />,
					shortcut: mac ? "⇧⌥F" : "Shift+Alt+F",
				},
			]);
	const groups = [
		editItems,
		navigationItems,
		supportedCodeActions,
		formatItems,
	].filter((group) => group.length > 0);

	return (
		<FloatingMenu
			open
			onOpenChange={(open) => !open && onClose()}
			reference={openAt}
			label="Editor actions"
			className="z-[140] min-w-[220px] rounded-md border border-border-subtle bg-bg-elevated py-1 text-[12px] shadow-2xl"
			gap={0}
		>
			{groups.map((group, groupIndex) => (
				<div key={group[0].key}>
					{groupIndex > 0 && (
						<div
							role="separator"
							className="my-1 border-t border-border-subtle"
						/>
					)}
					{group.map((item) => (
						<button
							key={item.key}
							type="button"
							role="menuitem"
							aria-label={item.label}
							disabled={!item.enabled}
							className="flex w-full items-center gap-2 px-3 py-1 text-left text-fg-muted transition-colors enabled:hover:bg-bg-hover enabled:hover:text-fg disabled:cursor-default disabled:opacity-40"
							onClick={() => {
								onClose();
								editor.focus();
								void item.run();
							}}
						>
							<span className="flex w-3.5 shrink-0 items-center justify-center text-fg-dim">
								{item.icon}
							</span>
							<span className="min-w-0 flex-1">{item.label}</span>
							{item.shortcut && (
								<span className="ml-4 text-[10px] text-fg-dim">
									{item.shortcut}
								</span>
							)}
						</button>
					))}
				</div>
			))}
		</FloatingMenu>
	);
}

function supportedActions(
	editor: MonacoTypes.editor.IStandaloneCodeEditor,
	definitions: ActionDefinition[],
): MenuItem[] {
	return definitions.flatMap((definition) => {
		const action = editor.getAction(definition.id);
		if (!action?.isSupported()) return [];
		return [
			{
				...definition,
				key: definition.id,
				enabled: true,
				run: () => action.run(),
			},
		];
	});
}

function languageActions(
	editor: MonacoTypes.editor.IStandaloneCodeEditor,
	definitions: LanguageActionDefinition[],
	supports: (feature: MonacoLanguageFeature) => boolean,
	showAlternates: boolean,
): MenuItem[] {
	return definitions.map(({ feature, alternate, ...definition }) => {
		const action = showAlternates && alternate ? alternate : definition;
		return {
			...action,
			key: definition.id,
			enabled: supports(feature),
			run: () => editor.trigger("wingman.contextMenu", action.id, null),
		};
	});
}

function commandItem(
	editor: MonacoTypes.editor.IStandaloneCodeEditor,
	id: string,
	label: string,
	icon: ReactNode,
	shortcut?: string,
	options: { enabled?: boolean } = {},
): MenuItem {
	return {
		key: id,
		label,
		icon,
		shortcut,
		enabled: options.enabled ?? true,
		run: () => editor.trigger("wingman.contextMenu", id, null),
	};
}
