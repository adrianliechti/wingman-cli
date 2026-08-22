import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import { getDebugOutput, type DebugInspection } from "../api/debug";
import { queryKeys } from "../api/query";
import {
	debugInspectionPollInterval,
	preserveDebugOutput,
} from "../debugInspection";

export function DebugOutputTab() {
	const outputRef = useRef<HTMLPreElement | null>(null);
	const followOutputRef = useRef(true);
	const query = useQuery<DebugInspection>({
		queryKey: queryKeys.debug.output,
		staleTime: 0,
		queryFn: ({ signal }) => getDebugOutput(signal),
		refetchInterval: (current) =>
			debugInspectionPollInterval(current.state.data, 500, 1_500),
		structuralSharing: (previous, next) =>
			preserveDebugOutput(previous, next as DebugInspection),
	});
	const output = query.data?.output ?? "";
	const error =
		query.data?.session?.error ||
		query.data?.error ||
		(query.error ? errorMessage(query.error) : "");

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
