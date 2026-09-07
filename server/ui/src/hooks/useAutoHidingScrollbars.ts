import { useEffect } from "react";

const SCROLL_KEYS = new Set([
	"ArrowUp",
	"ArrowDown",
	"PageUp",
	"PageDown",
	"Home",
	"End",
	" ",
]);

function scrollableAncestor(target: EventTarget | null): Element | null {
	let element = target instanceof Element ? target : null;
	while (element && element !== document.documentElement) {
		const style = getComputedStyle(element);
		if (
			/auto|scroll/.test(`${style.overflowX} ${style.overflowY}`) &&
			(element.scrollHeight > element.clientHeight + 1 ||
				element.scrollWidth > element.clientWidth + 1)
		)
			return element;
		element = element.parentElement;
	}
	return null;
}

// Scrollbar thumbs stay transparent during programmatic scrolling (notably
// while a chat streams) and briefly appear for direct user scroll gestures.
export function useAutoHidingScrollbars() {
	useEffect(() => {
		const timers = new Map<Element, number>();
		const reveal = (target: EventTarget | null) => {
			const element = scrollableAncestor(target);
			if (!element) return;
			const previous = timers.get(element);
			if (previous) window.clearTimeout(previous);
			element.classList.add("scrollbar-active");
			timers.set(
				element,
				window.setTimeout(() => {
					element.classList.remove("scrollbar-active");
					timers.delete(element);
				}, 800),
			);
		};
		const onPointer = (event: Event) => reveal(event.target);
		const onKey = (event: KeyboardEvent) => {
			if (SCROLL_KEYS.has(event.key)) reveal(event.target);
		};

		document.addEventListener("wheel", onPointer, {
			capture: true,
			passive: true,
		});
		document.addEventListener("touchmove", onPointer, {
			capture: true,
			passive: true,
		});
		document.addEventListener("pointerdown", onPointer, true);
		document.addEventListener("keydown", onKey, true);
		return () => {
			document.removeEventListener("wheel", onPointer, true);
			document.removeEventListener("touchmove", onPointer, true);
			document.removeEventListener("pointerdown", onPointer, true);
			document.removeEventListener("keydown", onKey, true);
			for (const [element, timer] of timers) {
				window.clearTimeout(timer);
				element.classList.remove("scrollbar-active");
			}
		};
	}, []);
}
