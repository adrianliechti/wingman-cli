import { Check, CloudLightning, Copy } from "lucide-react";
import { type ErrorInfo, useEffect, useRef, useState } from "react";

export function ErrorPanel({
	title,
	error,
	errorInfo,
	context,
}: {
	title: string;
	error: Error;
	errorInfo: ErrorInfo | null;
	context?: string;
}) {
	return (
		<div className="flex h-full min-h-0 w-full items-center justify-center bg-bg p-5">
			<section
				role="alert"
				aria-label={title}
				className="flex flex-col items-center gap-2.5 text-center"
			>
				<CloudLightning
					size={60}
					strokeWidth={1.45}
					aria-hidden="true"
					className="text-fg-dim"
				/>
				<CopyErrorButton
					error={error}
					errorInfo={errorInfo}
					context={context}
				/>
			</section>
		</div>
	);
}

function CopyErrorButton({
	error,
	errorInfo,
	context,
}: {
	error: Error;
	errorInfo: ErrorInfo | null;
	context?: string;
}) {
	const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
		"idle",
	);
	const resetTimer = useRef<number | null>(null);
	useEffect(
		() => () => {
			if (resetTimer.current !== null) window.clearTimeout(resetTimer.current);
		},
		[],
	);

	const details = formatErrorDetails(error, errorInfo, context);
	const copy = async () => {
		try {
			await writeClipboard(details);
			setCopyState("copied");
		} catch {
			setCopyState("failed");
		}
		if (resetTimer.current !== null) window.clearTimeout(resetTimer.current);
		resetTimer.current = window.setTimeout(() => setCopyState("idle"), 1800);
	};
	return (
		<button
			type="button"
			onClick={() => void copy()}
			aria-live="polite"
			className={`inline-flex h-7 items-center gap-1.5 rounded-md px-2.5 text-[11px] text-fg-dim transition-colors hover:bg-bg-hover hover:text-fg-muted ${copyState === "failed" ? "text-danger" : ""}`}
		>
			{copyState === "copied" ? <Check size={11} /> : <Copy size={11} />}
			{copyState === "copied"
				? "Copied"
				: copyState === "failed"
					? "Copy failed"
					: "Copy error"}
		</button>
	);
}

function formatErrorDetails(
	error: Error,
	errorInfo: ErrorInfo | null,
	context?: string,
) {
	const sections: string[] = [];
	const embeddedContext =
		"diagnosticContext" in error && typeof error.diagnosticContext === "string"
			? error.diagnosticContext
			: undefined;
	const diagnosticContext = context?.trim() || embeddedContext?.trim();
	if (diagnosticContext) sections.push(diagnosticContext);
	sections.push(
		error.stack?.trim()
			? `Client stack:\n${error.stack.trim()}`
			: `Client error:\n${error.name}: ${error.message}`,
	);
	if (errorInfo?.componentStack?.trim()) {
		sections.push(`React component stack:\n${errorInfo.componentStack.trim()}`);
	}
	sections.push(
		`Environment:\nPage: ${window.location.href}\nUser agent: ${navigator.userAgent}`,
	);
	return sections.join("\n\n");
}

async function writeClipboard(text: string) {
	try {
		if (navigator.clipboard?.writeText) {
			await navigator.clipboard.writeText(text);
			return;
		}
	} catch {
		// Fall through for embedded webviews where the Clipboard API exists but
		// is unavailable without a secure-context permission grant.
	}
	const textarea = document.createElement("textarea");
	textarea.value = text;
	textarea.setAttribute("readonly", "");
	textarea.style.position = "fixed";
	textarea.style.opacity = "0";
	document.body.appendChild(textarea);
	textarea.select();
	const copied = document.execCommand("copy");
	textarea.remove();
	if (!copied) throw new Error("clipboard unavailable");
}
