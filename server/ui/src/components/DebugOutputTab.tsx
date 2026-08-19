import { useCallback, useEffect, useRef, useState } from "react";
import { getDebugOutput } from "../api/debug";

export function DebugOutputTab() {
	const requestRef = useRef<AbortController | null>(null);
	const outputRef = useRef<HTMLPreElement | null>(null);
	const followOutputRef = useRef(true);
	const sessionIDRef = useRef<string | undefined>(undefined);
	const [output, setOutput] = useState("");
	const [error, setError] = useState("");

	const refresh = useCallback(async () => {
		requestRef.current?.abort();
		const controller = new AbortController();
		requestRef.current = controller;
		try {
			const next = await getDebugOutput(controller.signal);
			if (controller.signal.aborted) return null;
			const nextSessionID = next.session?.session_id;
			if (nextSessionID && nextSessionID !== sessionIDRef.current) {
				sessionIDRef.current = nextSessionID;
				setOutput(next.output);
			} else if (next.output) {
				// Keep the final snapshot when a manually stopped session disappears
				// from discovery; it remains useful until another session starts.
				setOutput(next.output);
			}
			setError(next.error ?? "");
			return next;
		} catch (cause) {
			if (!controller.signal.aborted) setError(errorMessage(cause));
			return null;
		} finally {
			if (requestRef.current === controller) requestRef.current = null;
		}
	}, []);

	useEffect(() => {
		let disposed = false;
		let timer = 0;
		const poll = async () => {
			const next = await refresh();
			if (disposed) return;
			const delay = next?.session?.state === "running" ? 500 : 1_500;
			timer = window.setTimeout(() => void poll(), delay);
		};
		void poll();
		return () => {
			disposed = true;
			window.clearTimeout(timer);
			requestRef.current?.abort();
		};
	}, [refresh]);

	useEffect(() => {
		const element = outputRef.current;
		if (element && followOutputRef.current)
			element.scrollTop = element.scrollHeight;
	}, [output]);

	return (
		<div className="flex h-full min-h-0 flex-col bg-bg">
			<pre
				ref={outputRef}
				onScroll={(event) => {
					const element = event.currentTarget;
					followOutputRef.current =
						element.scrollHeight - element.scrollTop - element.clientHeight <
						24;
				}}
				aria-label="Debug output"
				className="min-h-0 flex-1 overflow-auto whitespace-pre-wrap break-words bg-black/10 px-4 py-3 font-mono text-[12px] leading-relaxed text-fg-muted select-text"
			>
				{output || "No debugger output yet."}
			</pre>
			{error && (
				<div className="shrink-0 border-t border-danger/30 bg-danger/5 px-3 py-2 text-[11px] text-danger">
					{error}
				</div>
			)}
		</div>
	);
}

function errorMessage(value: unknown) {
	return value instanceof Error ? value.message : String(value);
}
