import { loader } from "@monaco-editor/react";
import * as monaco from "monaco-editor";
import editorWorker from "monaco-editor/editor/editor.worker?worker";
import cssWorker from "monaco-editor/languages/features/css/css.worker?worker";
import htmlWorker from "monaco-editor/languages/features/html/html.worker?worker";
import jsonWorker from "monaco-editor/languages/features/json/json.worker?worker";
import tsWorker from "monaco-editor/languages/features/typescript/ts.worker?worker";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./devicon-slim.css";
import "./index.css";
import App from "./App.tsx";
import { ToastProvider } from "./components/ui/Feedback.tsx";

// monaco-editor declares MonacoEnvironment inside its own module, so the name
// never reaches the global scope we assign it on.
declare global {
	interface Window {
		MonacoEnvironment?: monaco.Environment;
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

// The packaged macOS app runs in WKWebView. Its AppKit window extends the web
// content underneath the native traffic lights, so reserve their hit area.
// Chromium-based browsers and WebView2 keep their normal content bounds.
const userAgent = navigator.userAgent;
if (
	/Mac/.test(navigator.platform) &&
	/AppleWebKit/.test(userAgent) &&
	!/(Chrome|Chromium|CriOS|Edg|Electron|Safari)/.test(userAgent)
) {
	document.documentElement.dataset.windowChrome = "macos-overlay";
}

createRoot(document.getElementById("root")!).render(
	<StrictMode>
		<ToastProvider>
			<App />
		</ToastProvider>
	</StrictMode>,
);
