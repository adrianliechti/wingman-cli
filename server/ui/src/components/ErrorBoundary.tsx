import { Component, type ErrorInfo, type ReactNode } from "react";
import { ErrorPanel } from "./ErrorScreen";

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
			<ErrorPanel
				title="This view stopped rendering"
				error={error}
				errorInfo={errorInfo}
			/>
		);
	}
}
