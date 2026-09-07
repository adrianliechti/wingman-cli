import { useCallback, useReducer } from "react";
import { layoutReducer, type LayoutState } from "../mainLayout.ts";

// Navigation updates share one reducer so tabs and pane selections change together.
export function useMainLayout(initial: LayoutState) {
	const [layout, dispatchLayout] = useReducer(layoutReducer, initial);
	const setTabs = useCallback(
		(
			value:
				| LayoutState["tabs"]
				| ((previous: LayoutState["tabs"]) => LayoutState["tabs"]),
		) => dispatchLayout({ field: "tabs", value }),
		[],
	);
	const setActiveTabId = useCallback(
		(value: string | ((previous: string) => string)) =>
			dispatchLayout({ field: "activeTabId", value }),
		[],
	);
	const setLeftActiveId = useCallback(
		(value: string | ((previous: string) => string)) =>
			dispatchLayout({ field: "leftActiveId", value }),
		[],
	);
	const setRightActiveId = useCallback(
		(value: string | ((previous: string) => string)) =>
			dispatchLayout({ field: "rightActiveId", value }),
		[],
	);
	const setCurrentSessionId = useCallback(
		(value: string | ((previous: string) => string)) =>
			dispatchLayout({ field: "currentSessionId", value }),
		[],
	);
	return {
		...layout,
		setTabs,
		setActiveTabId,
		setLeftActiveId,
		setRightActiveId,
		setCurrentSessionId,
	};
}
