import { AlertTriangle, RefreshCw } from "lucide-react";
import type { ErrorInfo } from "react";
import { ErrorDetails } from "./components/ErrorBoundary";

export function AppCrashed({
	error,
	errorInfo,
	onReset,
}: {
	error: Error;
	errorInfo: ErrorInfo | null;
	onReset: () => void;
}) {
	return (
		<div className="flex h-screen w-screen flex-col items-center justify-center gap-4 bg-bg p-6 text-center">
			<AlertTriangle size={32} className="text-danger" />
			<div className="text-[14px] font-medium text-fg">
				Wingman ran into a problem and needs to recover.
			</div>
			<div className="flex gap-2">
				<button
					type="button"
					onClick={onReset}
					className="inline-flex h-9 items-center gap-1.5 rounded-md border border-border px-4 text-[12px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
				>
					<RefreshCw size={13} />
					Try to recover
				</button>
				<button
					type="button"
					onClick={() => window.location.reload()}
					className="inline-flex h-9 items-center gap-1.5 rounded-md bg-accent px-4 text-[12px] text-white transition-colors hover:opacity-90"
				>
					Reload Wingman
				</button>
			</div>
			<ErrorDetails error={error} errorInfo={errorInfo} />
		</div>
	);
}
