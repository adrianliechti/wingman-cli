import { Loader2 } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useColorScheme } from "../hooks/useColorScheme";

export function MermaidPreview({ text, path }: { text: string; path: string }) {
	const scheme = useColorScheme();
	const reactId = useId();
	const [imageURL, setImageURL] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		let active = true;
		let nextURL: string | null = null;
		setImageURL(null);
		setError(null);

		void import("mermaid")
			.then(async ({ default: mermaid }) => {
				mermaid.initialize({
					startOnLoad: false,
					securityLevel: "strict",
					suppressErrorRendering: true,
					theme: scheme === "dark" ? "dark" : "default",
				});
				const renderId = `mermaid-${reactId.replace(/[^a-zA-Z0-9_-]/g, "")}`;
				const { svg } = await mermaid.render(renderId, text);
				if (!active) return;
				nextURL = URL.createObjectURL(
					new Blob([svg], { type: "image/svg+xml;charset=utf-8" }),
				);
				setImageURL(nextURL);
			})
			.catch((reason: unknown) => {
				if (!active) return;
				setError(reason instanceof Error ? reason.message : String(reason));
			});

		return () => {
			active = false;
			if (nextURL) URL.revokeObjectURL(nextURL);
		};
	}, [reactId, scheme, text]);

	if (error) {
		return (
			<div
				role="alert"
				className="m-6 rounded-md border border-danger/30 bg-danger/5 p-4 text-[12px] text-danger"
			>
				Could not preview {path}: {error}
			</div>
		);
	}

	if (!imageURL) {
		return (
			<div className="flex h-full items-center justify-center text-fg-dim">
				<Loader2
					size={15}
					className="animate-spin"
					aria-label="Rendering diagram"
				/>
			</div>
		);
	}

	return (
		<div
			data-mermaid-preview
			className="flex h-full min-h-0 w-full items-center justify-center overflow-auto p-6"
		>
			<img
				src={imageURL}
				alt={`Preview of ${path}`}
				className="h-auto w-auto object-contain"
				style={{ maxWidth: "100%", maxHeight: "100%" }}
			/>
		</div>
	);
}
