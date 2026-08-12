export type TextPreviewKind =
	| "html"
	| "markdown"
	| "svg"
	| "json"
	| "yaml"
	| "toml"
	| "xml"
	| "mermaid"
	| "csv"
	| "tsv";

export function textPreviewKind(path: string): TextPreviewKind | null {
	const extension = path.toLowerCase().split(".").pop();
	switch (extension) {
		case "html":
		case "htm":
			return "html";
		case "md":
		case "markdown":
			return "markdown";
		case "svg":
		case "json":
		case "toml":
		case "xml":
		case "csv":
		case "tsv":
			return extension;
		case "yaml":
		case "yml":
			return "yaml";
		case "mmd":
		case "mermaid":
			return "mermaid";
		default:
			return null;
	}
}
