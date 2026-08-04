import { FitAddon } from "@xterm/addon-fit";
import { Terminal, type ITheme } from "@xterm/xterm";
import { useEffect, useRef } from "react";
import { useColorScheme } from "../hooks/useColorScheme";

interface Props {
	id: string;
	active: boolean;
	onExit: (id: string) => void;
	onTitle: (id: string, title: string) => void;
}

export function TerminalView({ id, active, onExit, onTitle }: Props) {
	const hostRef = useRef<HTMLDivElement>(null);
	const termRef = useRef<Terminal | null>(null);
	const fitRef = useRef<FitAddon | null>(null);
	const scheme = useColorScheme();

	const exitRef = useRef(onExit);
	exitRef.current = onExit;
	const titleRef = useRef(onTitle);
	titleRef.current = onTitle;

	useEffect(() => {
		const host = hostRef.current;
		if (!host) return;

		const term = new Terminal({
			fontFamily: cssVar("--font-mono", "monospace"),
			fontSize: 12,
			lineHeight: 1.25,
			cursorBlink: true,
			scrollback: 10000,
			theme: terminalTheme(),
		});
		const fit = new FitAddon();
		term.loadAddon(fit);
		term.open(host);
		safeFit(fit);

		termRef.current = term;
		fitRef.current = fit;

		const proto = location.protocol === "https:" ? "wss:" : "ws:";
		const ws = new WebSocket(
			`${proto}//${location.host}/api/terminals/${encodeURIComponent(id)}/ws`,
		);
		ws.binaryType = "arraybuffer";

		const pending: string[] = [];
		const send = (msg: Record<string, unknown>) => {
			const data = JSON.stringify(msg);
			if (ws.readyState === WebSocket.OPEN) ws.send(data);
			else if (ws.readyState === WebSocket.CONNECTING) pending.push(data);
		};

		ws.onopen = () => {
			for (const data of pending) ws.send(data);
			pending.length = 0;
			send({ type: "resize", cols: term.cols, rows: term.rows });
		};

		ws.onmessage = (e) => {
			if (typeof e.data === "string") {
				try {
					if (JSON.parse(e.data).type === "exit") exitRef.current(id);
				} catch {}
				return;
			}
			term.write(new Uint8Array(e.data as ArrayBuffer));
		};

		const dataSub = term.onData((data) => send({ type: "input", data }));
		const resizeSub = term.onResize(({ cols, rows }) =>
			send({ type: "resize", cols, rows }),
		);
		// OSC 0/2 — shells and full-screen apps name their window this way. The
		// scrollback replay re-emits the last one, so a reattach keeps the name.
		const titleSub = term.onTitleChange((title) =>
			titleRef.current(id, title.trim().slice(0, 80)),
		);

		const observer = new ResizeObserver(() => {
			if (host.clientWidth === 0 || host.clientHeight === 0) return;
			safeFit(fit);
		});
		observer.observe(host);

		return () => {
			observer.disconnect();
			dataSub.dispose();
			resizeSub.dispose();
			titleSub.dispose();
			ws.onmessage = null;
			ws.close();
			term.dispose();
			termRef.current = null;
			fitRef.current = null;
		};
	}, [id]);

	useEffect(() => {
		const term = termRef.current;
		if (term) term.options.theme = terminalTheme();
	}, [scheme]);

	useEffect(() => {
		if (!active) return;
		const handle = window.setTimeout(() => {
			if (fitRef.current) safeFit(fitRef.current);
			termRef.current?.focus();
		}, 0);
		return () => window.clearTimeout(handle);
	}, [active]);

	return <div ref={hostRef} className="h-full w-full px-2 py-1 bg-bg" />;
}

function safeFit(fit: FitAddon) {
	try {
		fit.fit();
	} catch {}
}

function cssVar(name: string, fallback: string): string {
	if (typeof window === "undefined") return fallback;
	const value = getComputedStyle(document.documentElement)
		.getPropertyValue(name)
		.trim();
	return value || fallback;
}

const DARK_ANSI: ITheme = {
	black: "#1c1c1c",
	red: "#f87171",
	green: "#34d399",
	yellow: "#fbbf24",
	blue: "#60a5fa",
	magenta: "#a78bfa",
	cyan: "#22d3ee",
	white: "#d4d4d4",
	brightBlack: "#525252",
	brightRed: "#fca5a5",
	brightGreen: "#6ee7b7",
	brightYellow: "#fcd34d",
	brightBlue: "#93c5fd",
	brightMagenta: "#c4b5fd",
	brightCyan: "#67e8f9",
	brightWhite: "#fafafa",
};

const LIGHT_ANSI: ITheme = {
	black: "#171717",
	red: "#dc2626",
	green: "#059669",
	yellow: "#b45309",
	blue: "#2563eb",
	magenta: "#7c3aed",
	cyan: "#0891b2",
	white: "#525252",
	brightBlack: "#737373",
	brightRed: "#ef4444",
	brightGreen: "#10b981",
	brightYellow: "#d97706",
	brightBlue: "#3b82f6",
	brightMagenta: "#8b5cf6",
	brightCyan: "#06b6d4",
	brightWhite: "#171717",
};

function terminalTheme(): ITheme {
	const light = window.matchMedia("(prefers-color-scheme: light)").matches;
	const background = cssVar("--color-bg", light ? "#ffffff" : "#0a0a0a");
	const foreground = cssVar("--color-fg", light ? "#171717" : "#e6e6e6");
	return {
		...(light ? LIGHT_ANSI : DARK_ANSI),
		background,
		foreground,
		cursor: foreground,
		cursorAccent: background,
		selectionBackground: cssVar(
			"--color-selection",
			light ? "rgba(0,0,0,0.15)" : "rgba(255,255,255,0.15)",
		),
	};
}
