import type { Monaco } from "@monaco-editor/react";
import type { ColorScheme } from "./hooks/useColorScheme";

let registered = false;

export function defineWingmanThemes(monaco: Monaco) {
	if (registered) return;
	registered = true;

	monaco.editor.defineTheme("wingman", {
		base: "vs-dark",
		inherit: true,
		rules: [
			{ token: "comment", foreground: "555555" },
			{ token: "keyword", foreground: "a78bfa" },
			{ token: "string", foreground: "34d399" },
			{ token: "number", foreground: "fbbf24" },
			{ token: "type", foreground: "60a5fa" },
		],
		colors: {
			"editor.background": "#0a0a0a",
			"editor.foreground": "#e0e0e0",
			"editor.lineHighlightBackground": "#111111",
			"editor.selectionBackground": "#ffffff26",
			"editor.inactiveSelectionBackground": "#ffffff15",
			"editorLineNumber.foreground": "#333333",
			"editorLineNumber.activeForeground": "#666666",
			"editorCursor.foreground": "#888888",
			"editor.lineHighlightBorder": "#00000000",
			"editorWidget.background": "#111111",
			"editorWidget.border": "#1e1e1e",
			"editorGutter.background": "#0a0a0a",
			"scrollbarSlider.background": "#ffffff14",
			"scrollbarSlider.hoverBackground": "#ffffff26",
			"scrollbarSlider.activeBackground": "#ffffff33",
			"diffEditor.insertedTextBackground": "#1f6f3f33",
			"diffEditor.removedTextBackground": "#7a282833",
			"diffEditor.insertedLineBackground": "#1f6f3f1f",
			"diffEditor.removedLineBackground": "#7a28281f",
		},
	});

	monaco.editor.defineTheme("wingman-light", {
		base: "vs",
		inherit: true,
		rules: [
			{ token: "comment", foreground: "999999" },
			{ token: "keyword", foreground: "7c3aed" },
			{ token: "string", foreground: "059669" },
			{ token: "number", foreground: "d97706" },
			{ token: "type", foreground: "2563eb" },
		],
		colors: {
			"editor.background": "#ffffff",
			"editor.foreground": "#1a1a1a",
			"editor.lineHighlightBackground": "#f7f7f7",
			"editor.selectionBackground": "#0000001a",
			"editor.inactiveSelectionBackground": "#0000000d",
			"editorLineNumber.foreground": "#cccccc",
			"editorLineNumber.activeForeground": "#666666",
			"editorCursor.foreground": "#555555",
			"editor.lineHighlightBorder": "#00000000",
			"editorWidget.background": "#f7f7f7",
			"editorWidget.border": "#e5e5e5",
			"editorGutter.background": "#ffffff",
			"scrollbarSlider.background": "#00000014",
			"scrollbarSlider.hoverBackground": "#00000026",
			"scrollbarSlider.activeBackground": "#00000033",
			"diffEditor.insertedTextBackground": "#10b98140",
			"diffEditor.removedTextBackground": "#ef444440",
			"diffEditor.insertedLineBackground": "#10b98120",
			"diffEditor.removedLineBackground": "#ef444420",
		},
	});
}

export function wingmanThemeName(scheme: ColorScheme): string {
	return scheme === "light" ? "wingman-light" : "wingman";
}
