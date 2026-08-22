import { Loader2 } from "lucide-react";
import { useEffect, useId, useState } from "react";
import mermaidAssetURL from "mermaid/dist/mermaid.min.js?url";
import { useColorScheme } from "../hooks/useColorScheme";
import { PanZoomCanvas } from "./PanZoomCanvas";

type Mermaid = (typeof import("mermaid"))["default"];

let mermaidPromise: Promise<Mermaid> | null = null;

function prepareSVG(svg: string) {
	// Mermaid's C4 renderer currently emits xlink:href for person icons without
	// declaring the namespace. Inline HTML accepts it, but an isolated SVG image
	// is correctly rejected as malformed XML, leaving an otherwise empty canvas.
	let normalized =
		svg.includes("xlink:href") && !svg.includes("xmlns:xlink")
			? svg.replace(/<svg\b/, '<svg xmlns:xlink="http://www.w3.org/1999/xlink"')
			: svg;
	const document = new DOMParser().parseFromString(normalized, "image/svg+xml");
	const root = document.documentElement;
	if (root.getAttribute("aria-roledescription") === "c4") {
		const groups = Array.from(root.children).filter(
			(child): child is SVGGElement => child.tagName.toLowerCase() === "g",
		);
		const relationships = groups.filter((group) =>
			Array.from(group.children).some(
				(child) => child.getAttribute("marker-end") !== null,
			),
		);
		const firstNode = groups.find(
			(group) =>
				!relationships.includes(group) &&
				Array.from(group.children).some(
					(child) =>
						child.tagName.toLowerCase() === "rect" &&
						child.getAttribute("fill") !== "none",
				),
		);
		if (firstNode) {
			for (const relationship of relationships) {
				root.insertBefore(relationship, firstNode);
			}
			normalized = new XMLSerializer().serializeToString(root);
		}
	}
	const viewBox = (root.getAttribute("viewBox") ?? "")
		.trim()
		.split(/[\s,]+/)
		.map(Number);
	const width = viewBox[2];
	const height = viewBox[3];
	const size =
		Number.isFinite(width) && Number.isFinite(height) && width > 0 && height > 0
			? { width, height }
			: null;
	return { normalized, size };
}

function loadMermaid(): Promise<Mermaid> {
	const loaded = (globalThis as typeof globalThis & { mermaid?: Mermaid })
		.mermaid;
	if (loaded) return Promise.resolve(loaded);
	if (mermaidPromise) return mermaidPromise;

	mermaidPromise = new Promise<Mermaid>((resolve, reject) => {
		const script = document.createElement("script");
		script.src = mermaidAssetURL;
		script.async = true;
		script.onload = () => {
			const mermaid = (globalThis as typeof globalThis & { mermaid?: Mermaid })
				.mermaid;
			if (mermaid) {
				resolve(mermaid);
			} else {
				reject(new Error("Mermaid loaded without exposing its API"));
			}
		};
		script.onerror = () => reject(new Error("Could not load Mermaid"));
		document.head.append(script);
	}).catch((error: unknown) => {
		mermaidPromise = null;
		throw error;
	});

	return mermaidPromise;
}

export function MermaidPreview({ text, path }: { text: string; path: string }) {
	const scheme = useColorScheme();
	const reactId = useId();
	const [imageURL, setImageURL] = useState<string | null>(null);
	const [size, setSize] = useState<{ width: number; height: number } | null>(
		null,
	);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		let active = true;
		let nextURL: string | null = null;
		setImageURL(null);
		setSize(null);
		setError(null);

		void loadMermaid()
			.then(async (mermaid) => {
				mermaid.initialize({
					startOnLoad: false,
					securityLevel: "strict",
					suppressErrorRendering: true,
					theme: scheme === "dark" ? "dark" : "default",
				});
				const renderId = `mermaid-${reactId.replace(/[^a-zA-Z0-9_-]/g, "")}`;
				const { svg } = await mermaid.render(renderId, text);
				if (!active) return;
				const prepared = prepareSVG(svg);
				nextURL = URL.createObjectURL(
					new Blob([prepared.normalized], {
						type: "image/svg+xml;charset=utf-8",
					}),
				);
				setSize(prepared.size);
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
		<div data-mermaid-preview className="h-full min-h-0 w-full">
			<PanZoomCanvas
				width={size?.width ?? 0}
				height={size?.height ?? 0}
				fitKey={size ? imageURL : null}
				wheelMode="zoom-only"
			>
				<img
					src={imageURL}
					alt={`Preview of ${path}`}
					draggable={false}
					onLoad={(event) => {
						if (size) return;
						const image = event.currentTarget;
						setSize({
							width: image.naturalWidth || 800,
							height: image.naturalHeight || 600,
						});
					}}
					onError={() => setError(`Could not load the rendered ${path}`)}
					className="absolute top-0 left-0 max-w-none"
					style={size ? { width: size.width, height: size.height } : undefined}
				/>
			</PanZoomCanvas>
		</div>
	);
}
