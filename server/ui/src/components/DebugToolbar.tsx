import {
	ArrowDownToDot,
	Pause,
	Play,
	RedoDot,
	Square,
	StepBack,
	UndoDot,
} from "lucide-react";
import type { ReactNode } from "react";
import type { DebugSession } from "../api/debug";

export type DebugOperation =
	| "continue"
	| "next"
	| "stepIn"
	| "stepOut"
	| "stepBack"
	| "pause"
	| "stop";

interface Props {
	session?: DebugSession;
	busy: boolean;
	onControl: (operation: DebugOperation) => void;
}

export function DebugToolbar({ session, busy, onControl }: Props) {
	if (!session || session.state === "terminated") return null;

	const stopped = session.state === "stopped";
	const running = session.state === "running";
	return (
		<div
			className="flex min-w-0 flex-1 items-center gap-0.5"
			role="toolbar"
			aria-label="Debug controls"
		>
			{stopped ? (
				<>
					<Control
						label="Continue"
						disabled={busy}
						onClick={() => onControl("continue")}
					>
						<Play size={12} />
					</Control>
					<Control
						label="Step over"
						disabled={busy}
						onClick={() => onControl("next")}
					>
						<RedoDot size={12} />
					</Control>
					<Control
						label="Step into"
						disabled={busy}
						onClick={() => onControl("stepIn")}
					>
						<ArrowDownToDot size={12} />
					</Control>
					<Control
						label="Step out"
						disabled={busy}
						onClick={() => onControl("stepOut")}
					>
						<UndoDot size={12} />
					</Control>
					{session.capabilities.supports_step_back && (
						<Control
							label="Step back"
							disabled={busy}
							onClick={() => onControl("stepBack")}
						>
							<StepBack size={12} />
						</Control>
					)}
				</>
			) : running ? (
				<Control
					label="Pause"
					disabled={busy}
					onClick={() => onControl("pause")}
				>
					<Pause size={11} />
				</Control>
			) : null}
			<Control label="Stop" disabled={busy} onClick={() => onControl("stop")}>
				<Square size={10} />
			</Control>
		</div>
	);
}

function Control({
	label,
	disabled,
	onClick,
	children,
}: {
	label: string;
	disabled: boolean;
	onClick: () => void;
	children: ReactNode;
}) {
	return (
		<button
			type="button"
			disabled={disabled}
			title={label}
			aria-label={label}
			onClick={onClick}
			className="flex h-6 min-w-6 shrink-0 items-center justify-center rounded px-1 text-fg-dim hover:bg-bg-hover hover:text-fg disabled:opacity-40"
		>
			{children}
		</button>
	);
}
