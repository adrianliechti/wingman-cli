import type { GraphNode } from "../../api/insights";

export function nodeLocation(node: GraphNode) {
	return `${node.file}:${node.start_line}`;
}

export function nodeTargetLine(node: GraphNode) {
	return node.name_line !== undefined ? node.name_line + 1 : node.start_line;
}
