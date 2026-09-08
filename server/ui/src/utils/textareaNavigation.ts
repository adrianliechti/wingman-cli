// Textareas do not expose their caret's visual row. Measure it in an offscreen
// copy so history navigation respects word wrapping as well as explicit lines.
export function atTextareaEdge(
	input: HTMLTextAreaElement,
	edge: "first" | "last",
): boolean {
	if (!input.value) return true;
	const style = getComputedStyle(input);
	const mirror = document.createElement("div");
	Object.assign(mirror.style, {
		position: "fixed",
		visibility: "hidden",
		pointerEvents: "none",
		top: "-10000px",
		width: `${input.clientWidth}px`,
		boxSizing: "border-box",
		padding: style.padding,
		font: style.font,
		lineHeight: style.lineHeight,
		letterSpacing: style.letterSpacing,
		wordSpacing: style.wordSpacing,
		tabSize: style.tabSize,
		whiteSpace: "pre-wrap",
		overflowWrap: "break-word",
	});
	mirror.textContent = input.value.slice(0, input.selectionStart);
	const caret = document.createElement("span");
	caret.textContent = input.value.slice(input.selectionStart) || ".";
	mirror.append(caret);
	document.body.append(mirror);
	try {
		const lineHeight =
			parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.2;
		const top = caret.offsetTop;
		return edge === "first"
			? top < parseFloat(style.paddingTop) + lineHeight / 2
			: top + lineHeight * 1.5 >=
					mirror.clientHeight - parseFloat(style.paddingBottom);
	} finally {
		mirror.remove();
	}
}
