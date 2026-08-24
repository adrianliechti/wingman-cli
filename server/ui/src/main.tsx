import { loader } from "@monaco-editor/react";
import { QueryClientProvider } from "@tanstack/react-query";
import * as monaco from "monaco-editor";
import editorWorker from "monaco-editor/editor/editor.worker?worker";
import { StandaloneServices } from "monaco-editor/editor/standalone/browser/standaloneServices";
import cssWorker from "monaco-editor/languages/features/css/css.worker?worker";
import htmlWorker from "monaco-editor/languages/features/html/html.worker?worker";
import jsonWorker from "monaco-editor/languages/features/json/json.worker?worker";
import tsWorker from "monaco-editor/languages/features/typescript/ts.worker?worker";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./devicon-slim.css";
import "./index.css";
import App from "./App.tsx";
import { serverQueryClient } from "./api/query.ts";
import { AppCrashed } from "./AppCrashed.tsx";
import { ErrorBoundary } from "./components/ErrorBoundary.tsx";
import { ToastProvider } from "./components/ui/Feedback.tsx";

// monaco-editor declares MonacoEnvironment inside its own module, so the name
// never reaches the global scope we assign it on.
declare global {
	interface Window {
		MonacoEnvironment?: monaco.Environment;
		shell?: Readonly<{
			platform: "macos" | "windows";
			titleBar: Readonly<{
				overlay: boolean;
				height: number;
				insets: Readonly<{ left: number; right: number }>;
				maximized: boolean;
			}>;
		}>;
	}
}

self.MonacoEnvironment = {
	getWorker(_workerId, label) {
		if (label === "json") return new jsonWorker();
		if (label === "css" || label === "scss" || label === "less")
			return new cssWorker();
		if (label === "html" || label === "handlebars" || label === "razor")
			return new htmlWorker();
		if (label === "typescript" || label === "javascript") return new tsWorker();
		return new editorWorker();
	},
};

loader.config({ monaco });

// Monaco's standalone layout service parents hover tooltips and dropdowns
// inside the active editor's container, where the app's overflow-hidden panels
// clip them at the tab-bar edge (and the clipped box swallows clicks on the
// find widget's buttons). Serve them from a click-through viewport overlay
// instead; the monaco-component class keeps Monaco's theme variables in scope.
let monacoOverlay: HTMLElement | null = null;
function monacoOverlayContainer(): HTMLElement {
	if (!monacoOverlay) {
		monacoOverlay = document.createElement("div");
		monacoOverlay.className = "monaco-component";
		monacoOverlay.style.position = "fixed";
		monacoOverlay.style.inset = "0";
		monacoOverlay.style.zIndex = "90";
		monacoOverlay.style.pointerEvents = "none";
		document.body.appendChild(monacoOverlay);
	}
	return monacoOverlay;
}
const overlayDimension = () => ({
	width: window.innerWidth,
	height: window.innerHeight,
});
const noEvent = () => ({ dispose() {} });
StandaloneServices.initialize({
	layoutService: {
		onDidLayoutMainContainer: noEvent,
		onDidLayoutActiveContainer: noEvent,
		onDidLayoutContainer: noEvent,
		onDidChangeActiveContainer: noEvent,
		onDidAddContainer: noEvent,
		mainContainerOffset: { top: 0, quickPickTop: 0 },
		activeContainerOffset: { top: 0, quickPickTop: 0 },
		get mainContainer() {
			return monacoOverlayContainer();
		},
		get activeContainer() {
			return monacoOverlayContainer();
		},
		get containers() {
			return [monacoOverlayContainer()];
		},
		get mainContainerDimension() {
			return overlayDimension();
		},
		get activeContainerDimension() {
			return overlayDimension();
		},
		getContainer: () => monacoOverlayContainer(),
		whenContainerStylesLoaded: () => undefined,
		focus: () => {},
	},
});

createRoot(document.getElementById("root")!).render(
	<StrictMode>
		<QueryClientProvider client={serverQueryClient}>
			<ErrorBoundary
				fallback={(error, _reset, errorInfo) => (
					<AppCrashed error={error} errorInfo={errorInfo} />
				)}
			>
				<ToastProvider>
					<App />
				</ToastProvider>
			</ErrorBoundary>
		</QueryClientProvider>
	</StrictMode>,
);
