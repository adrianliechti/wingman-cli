export function collectionEntries(value: unknown): [string, unknown][] {
	if (Array.isArray(value)) {
		return value.map((child, index) => [String(index), child]);
	}
	if (value !== null && typeof value === "object" && !(value instanceof Date)) {
		return Object.entries(value);
	}
	return [];
}

export function collectionSummary(value: unknown): string {
	if (Array.isArray(value)) return `Array(${value.length})`;
	if (value !== null && typeof value === "object" && !(value instanceof Date)) {
		return `Object(${Object.keys(value).length})`;
	}
	return formatScalar(value);
}

export function formatScalar(value: unknown): string {
	if (value === null) return "null";
	if (value === undefined) return "undefined";
	if (typeof value === "string") return JSON.stringify(value);
	if (
		typeof value === "number" ||
		typeof value === "boolean" ||
		typeof value === "bigint"
	) {
		return String(value);
	}
	if (value instanceof Date) {
		return Number.isNaN(value.getTime()) ? "Invalid date" : value.toISOString();
	}
	if (typeof value === "object") {
		return Array.isArray(value) ? "[]" : "{}";
	}
	return String(value);
}

export function scalarClass(value: unknown): string {
	if (value === null || value === undefined) return "text-fg-dim";
	if (typeof value === "string") return "text-success";
	if (typeof value === "number" || typeof value === "bigint") {
		return "text-info";
	}
	if (typeof value === "boolean") return "text-purple";
	if (value instanceof Date) return "text-orange";
	return "text-fg-muted";
}
