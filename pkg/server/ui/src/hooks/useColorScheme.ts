import { useEffect, useState } from "react";

export type ColorScheme = "dark" | "light";

function currentScheme(): ColorScheme {
	if (typeof window === "undefined") return "dark";
	return window.matchMedia("(prefers-color-scheme: light)").matches
		? "light"
		: "dark";
}

export function useColorScheme(): ColorScheme {
	const [scheme, setScheme] = useState<ColorScheme>(currentScheme);

	useEffect(() => {
		const mq = window.matchMedia("(prefers-color-scheme: light)");
		const handler = (e: MediaQueryListEvent) =>
			setScheme(e.matches ? "light" : "dark");
		mq.addEventListener("change", handler);
		return () => mq.removeEventListener("change", handler);
	}, []);

	return scheme;
}
