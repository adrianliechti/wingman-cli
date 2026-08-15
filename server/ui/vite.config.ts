import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const monacoLang = /monaco-editor\/esm\/vs\/(basic-languages|languages?)\//;

function patchMonacoReactDiffDisposal() {
	const modelsFirst =
		"g||i?.original?.dispose(),N||i?.modified?.dispose(),u.current?.dispose()";
	const editorFirst =
		"u.current?.dispose(),g||i?.original?.dispose(),N||i?.modified?.dispose()";
	return {
		name: "wingman:monaco-react-diff-disposal",
		enforce: "pre" as const,
		transform(code: string, id: string) {
			const normalizedId = id.replaceAll("\\", "/");
			if (!normalizedId.includes("/@monaco-editor/react/dist/index.mjs")) {
				return;
			}
			const occurrences = code.split(modelsFirst).length - 1;
			if (occurrences !== 1) {
				throw new Error(
					`Expected one @monaco-editor/react diff disposal site, found ${occurrences}`,
				);
			}
			return code.replace(modelsFirst, editorFirst);
		},
	};
}

export default defineConfig({
	plugins: [patchMonacoReactDiffDisposal(), react(), tailwindcss()],
	// Keep this dependency in Vite's normal transform pipeline so the lifecycle
	// patch above is applied consistently in development and production.
	optimizeDeps: { exclude: ["@monaco-editor/react"] },
	build: {
		outDir: "../static",
		emptyOutDir: true,
		rolldownOptions: {
			output: {
				entryFileNames: "assets/[name].js",
				chunkFileNames: (chunk) =>
					monacoLang.test(chunk.facadeModuleId ?? "")
						? "assets/lang/[name].js"
						: "assets/[name].js",
				assetFileNames: "assets/[name].[ext]",
				codeSplitting: {
					groups: [
						{
							name: "editor",
							test: /node_modules[/\\]monaco-editor[/\\]esm[/\\]vs[/\\](?!basic-languages|language)/,
						},
					],
				},
			},
		},
	},
	worker: {
		rolldownOptions: {
			output: {
				entryFileNames: "assets/workers/[name].js",
				chunkFileNames: "assets/workers/[name].js",
			},
		},
	},
	server: {
		proxy: {
			"/api": { target: "http://localhost:4242", ws: true },
			"/ws": { target: "ws://localhost:4242", ws: true },
		},
	},
});
