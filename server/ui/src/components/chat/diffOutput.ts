const UNIFIED_OLD_HEADER = /^---(?:\s|$)/;
const UNIFIED_NEW_HEADER = /^\+\+\+(?:\s|$)/;

function isChangedLine(line: string): boolean {
	return (
		(line.startsWith("+") && !UNIFIED_NEW_HEADER.test(line)) ||
		(line.startsWith("-") && !UNIFIED_OLD_HEADER.test(line))
	);
}

export function looksLikeDiffPath(line: string): boolean {
	const value = line.trim();
	if (!value || value.length > 500 || value.includes("://")) return false;
	return (
		value.startsWith("/") ||
		value.startsWith("./") ||
		value.startsWith("../") ||
		/^[A-Za-z]:\\/.test(value) ||
		(value.includes("/") && !/\s/.test(value)) ||
		(value.includes("\\") && !/\s/.test(value)) ||
		/(?:^|[/\\])[^/\\]+\.[A-Za-z0-9_-]+$/.test(value)
	);
}

/** Detect standard patches and ACP's flattened path + prefixed-line diffs. */
export function looksLikeDiffOutput(text: string): boolean {
	const lines = text.replace(/\r\n?/g, "\n").split("\n");
	const hasChanges = lines.some(isChangedLine);
	if (!hasChanges) return false;

	const hasOldHeader = lines.some((line) => UNIFIED_OLD_HEADER.test(line));
	const hasNewHeader = lines.some((line) => UNIFIED_NEW_HEADER.test(line));
	if (
		lines.some((line) => line.startsWith("diff --git ")) ||
		lines.some((line) => /^@@(?:\s|$)/.test(line)) ||
		lines.some((line) => line === "*** Begin Patch") ||
		(hasOldHeader && hasNewHeader)
	) {
		return true;
	}

	let paths = 0;
	let prefixed = 0;
	for (const line of lines) {
		if (!line) continue;
		if (line.startsWith("+") || line.startsWith("-") || line.startsWith(" ")) {
			prefixed++;
			continue;
		}
		if (looksLikeDiffPath(line)) {
			paths++;
			continue;
		}
		return false;
	}
	return paths > 0 && prefixed > 0;
}

export function shouldRenderDiff(
	kind: string | undefined,
	name: string | undefined,
	text: string,
): boolean {
	if (kind === "edit" || kind === "delete" || kind === "move") return true;
	if (name === "edit" || name === "write") return true;
	return looksLikeDiffOutput(text);
}
