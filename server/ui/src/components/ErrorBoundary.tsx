import { AlertTriangle, RefreshCw } from "lucide-react";
import { Component, type ErrorInfo, type ReactNode } from "react";

interface Props {
	children: ReactNode;
	fallback?: (
		error: Error,
		reset: () => void,
		errorInfo: ErrorInfo | null,
	) => ReactNode;
	onError?: (error: Error, info: ErrorInfo) => void;
}

interface State {
	error: Error | null;
	errorInfo: ErrorInfo | null;
}

export class ErrorBoundary extends Component<Props, State> {
	state: State = { error: null, errorInfo: null };

	static getDerivedStateFromError(error: Error): Pick<State, "error"> {
		return { error };
	}

	componentDidCatch(error: Error, info: ErrorInfo) {
		console.error("Wingman UI crashed:", error, info.componentStack);
		this.setState({ errorInfo: info });
		this.props.onError?.(error, info);
	}

	reset = () => this.setState({ error: null, errorInfo: null });

	render() {
		const { error, errorInfo } = this.state;
		if (!error) return this.props.children;
		if (this.props.fallback) {
			return this.props.fallback(error, this.reset, errorInfo);
		}
		return (
			<DefaultFallback
				error={error}
				errorInfo={errorInfo}
				onReset={this.reset}
			/>
		);
	}
}

function DefaultFallback({
	error,
	errorInfo,
	onReset,
}: {
	error: Error;
	errorInfo: ErrorInfo | null;
	onReset: () => void;
}) {
	return (
		<div className="flex h-full w-full flex-col items-center justify-center gap-3 p-6 text-center">
			<AlertTriangle size={28} className="text-danger" />
			<div className="max-w-md text-[13px] text-fg">Something went wrong.</div>
			<button
				type="button"
				onClick={onReset}
				className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-3 text-[12px] text-fg-muted transition-colors hover:bg-bg-hover hover:text-fg"
			>
				<RefreshCw size={12} />
				Try again
			</button>
			<ErrorDetails error={error} errorInfo={errorInfo} />
		</div>
	);
}

export function ErrorDetails({
	error,
	errorInfo,
	className,
}: {
	error: Error;
	errorInfo: ErrorInfo | null;
	className?: string;
}) {
	const details = [error.stack ?? error.message, errorInfo?.componentStack]
		.filter(Boolean)
		.join("\n\ncomponent stack:\n");
	if (!details) return null;
	return (
		<details className={`w-full max-w-lg text-left ${className ?? ""}`}>
			<summary className="cursor-pointer text-center text-[11px] text-fg-dim hover:text-fg-muted">
				Show details
			</summary>
			<pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap rounded-md border border-border bg-bg-elevated p-2 text-left text-[10px] leading-relaxed text-fg-dim">
				{details}
			</pre>
		</details>
	);
}
