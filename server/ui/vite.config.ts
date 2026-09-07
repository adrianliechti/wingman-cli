import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

const monacoLang = /monaco-editor\/esm\/vs\/(basic-languages|languages?)\//;

export default defineConfig({
	plugins: [react(), tailwindcss()],
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
		},
	},
});
