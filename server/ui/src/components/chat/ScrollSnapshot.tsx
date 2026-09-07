import { Component } from "react";
import type { Phase } from "../../types/protocol";

interface Props {
	phase: Phase;
	capture: () => void;
	restore: () => void;
}

// Capture the transcript's scroll anchor immediately before automatic collapse
// changes the DOM. Function components have no equivalent snapshot lifecycle.
export class ScrollSnapshot extends Component<Props> {
	getSnapshotBeforeUpdate(previous: Props) {
		const collapsing = previous.phase !== "idle" && this.props.phase === "idle";
		if (collapsing) this.props.capture();
		return collapsing;
	}

	componentDidUpdate(
		_previous: Props,
		_previousState: unknown,
		collapsing: boolean,
	) {
		if (collapsing) this.props.restore();
	}

	render() {
		return null;
	}
}
