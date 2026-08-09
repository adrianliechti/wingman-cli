import { useSyncExternalStore } from "react";

export type ColorScheme = "dark" | "light";

const QUERY = "(prefers-color-scheme: light)";
const listeners = new Set<() => void>();
let mediaQuery: MediaQueryList | undefined;

function query(): MediaQueryList {
	mediaQuery ??= window.matchMedia(QUERY);
	return mediaQuery;
}

function systemScheme(): ColorScheme {
	if (typeof window === "undefined") return "dark";
	return query().matches ? "light" : "dark";
}

function applyScheme(scheme: ColorScheme) {
	const root = document.documentElement;
	root.dataset.theme = scheme;
	root.style.colorScheme = scheme;
	root.classList.toggle("dark", scheme === "dark");
	const favicon = document.querySelector<HTMLLinkElement>(
		"link[data-wingman-favicon]",
	);
	if (favicon) {
		favicon.href = scheme === "dark" ? "/icon_dark.svg" : "/icon_light.svg";
	}
}

function notify() {
	applyScheme(systemScheme());
	for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
	if (listeners.size === 0) {
		query().addEventListener("change", notify);
	}
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
		if (listeners.size === 0) {
			query().removeEventListener("change", notify);
		}
	};
}

export function useColorScheme(): ColorScheme {
	return useSyncExternalStore(subscribe, systemScheme, () => "dark");
}
