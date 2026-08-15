export interface TodoItem {
	content: string;
	status?: string;
}

function completeJsonPrefix(s: string): string | null {
	const stack: string[] = [];
	let inString = false;
	let escaped = false;

	for (const c of s) {
		if (inString) {
			if (escaped) escaped = false;
			else if (c === "\\") escaped = true;
			else if (c === '"') inString = false;
			continue;
		}
		if (c === '"') inString = true;
		else if (c === "{" || c === "[") stack.push(c);
		else if (c === "}" || c === "]") {
			const open = stack.at(-1);
			if (!open || (c === "}" && open !== "{") || (c === "]" && open !== "["))
				return null;
			stack.pop();
		}
	}

	if (!inString && !stack.length) return null;

	let out = s;
	if (escaped) out = out.slice(0, -1);
	if (inString) out += '"';

	out = out.trimEnd();
	if (out.endsWith(":")) out += "null";
	else if (out.endsWith(",")) out = out.slice(0, -1);

	for (let i = stack.length - 1; i >= 0; i--) {
		out += stack[i] === "{" ? "}" : "]";
	}
	return out;
}

export function parseTodoItems(raw?: string): TodoItem[] {
	if (!raw) return [];
	for (const candidate of [raw, completeJsonPrefix(raw)]) {
		if (!candidate) continue;
		try {
			const items: unknown = JSON.parse(candidate).items;
			if (Array.isArray(items)) {
				return items.filter(
					(it): it is TodoItem =>
						typeof it?.content === "string" && it.content.trim() !== "",
				);
			}
		} catch {}
	}
	return [];
}
