import { useSyncExternalStore } from "react";

const query = matchMedia("(pointer: coarse) and (max-width: 1024px)");
const subscribe = (listener: () => void) => {
	query.addEventListener("change", listener);
	return () => query.removeEventListener("change", listener);
};
const getSnapshot = () => query.matches;

// A small desktop window still has the desktop controls.
export function useMobileLayout() {
	return useSyncExternalStore(subscribe, getSnapshot);
}
