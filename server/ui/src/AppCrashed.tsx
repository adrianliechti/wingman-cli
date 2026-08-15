import type { ErrorInfo } from "react";
import { ErrorPanel } from "./components/ErrorScreen";

export function AppCrashed({
	error,
	errorInfo,
}: {
	error: Error;
	errorInfo: ErrorInfo | null;
}) {
	return (
		<ErrorPanel
			title="Wingman hit an unexpected error"
			error={error}
			errorInfo={errorInfo}
		/>
	);
}
