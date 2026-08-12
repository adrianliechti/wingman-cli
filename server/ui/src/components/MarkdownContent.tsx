import { streamingMarkdownExtension } from "@tanstack/markdown/extensions/streaming";
import {
	Markdown,
	type MarkdownComponentProps,
	type MarkdownComponents,
} from "@tanstack/markdown/react";
import { Check, Copy } from "lucide-react";
import * as monaco from "monaco-editor";
import {
	Children,
	isValidElement,
	memo,
	useEffect,
	useMemo,
	useRef,
	useState,
	type ReactElement,
	type ReactNode,
} from "react";

const STREAMING_EXTENSIONS = [streamingMarkdownExtension()];

const LANGUAGE_ALIASES: Record<string, string> = {
	bash: "shell",
	"c++": "cpp",
	cs: "csharp",
	golang: "go",
	js: "javascript",
	jsx: "javascript",
	md: "markdown",
	plaintext: "plaintext",
	py: "python",
	rb: "ruby",
	sh: "shell",
	text: "plaintext",
	ts: "typescript",
	tsx: "typescript",
	yml: "yaml",
	zsh: "shell",
};

const languageLoads = new Map<string, Promise<string | null>>();

function resolveMonacoLanguage(language: string) {
	const requested = language.trim().toLowerCase();
	const normalized = LANGUAGE_ALIASES[requested] ?? requested;
	if (!normalized || normalized === "plaintext") return null;

	return (
		monaco.languages.getLanguages().find((candidate) => {
			if (candidate.id.toLowerCase() === normalized) return true;
			if (
				candidate.aliases?.some((alias) => alias.toLowerCase() === normalized)
			)
				return true;
			return candidate.extensions?.some(
				(extension) => extension.slice(1).toLowerCase() === normalized,
			);
		})?.id ?? null
	);
}

function loadMonacoLanguage(language: string) {
	const languageId = resolveMonacoLanguage(language);
	if (!languageId) return Promise.resolve(null);

	let load = languageLoads.get(languageId);
	if (!load) {
		// Monaco's public colorize API waits for the language's lazy tokenizer.
		// Discarding its empty HTML result lets us render safe React nodes below.
		load = monaco.editor
			.colorize("", languageId, {})
			.then(() => languageId)
			.catch(() => null);
		languageLoads.set(languageId, load);
	}
	return load;
}

function tokenClass(type: string) {
	if (type.includes("comment")) return "md-token-comment";
	if (type.includes("string")) return "md-token-string";
	if (type.includes("regexp")) return "md-token-regexp";
	if (type.includes("keyword")) return "md-token-keyword";
	if (type.includes("number") || type.includes("numeric"))
		return "md-token-number";
	if (/(^|\.)(type|class|namespace)(\.|$)/.test(type)) return "md-token-type";
	if (type.includes("attribute")) return "md-token-attribute";
	if (type.includes("tag")) return "md-token-tag";
	if (type.includes("function")) return "md-token-function";
	if (type.includes("variable")) return "md-token-variable";
	if (type.includes("operator") || type.includes("delimiter"))
		return "md-token-operator";
	return undefined;
}

function highlightedCode(code: string, languageId: string): ReactNode[] {
	const lines = code.split("\n");
	const tokenLines = monaco.editor.tokenize(code, languageId);

	return lines.flatMap((line, lineIndex) => {
		const tokens = tokenLines[lineIndex] ?? [];
		const content: ReactNode[] = tokens.map((token, tokenIndex) => {
			const end = tokens[tokenIndex + 1]?.offset ?? line.length;
			return (
				<span
					key={`${lineIndex}:${token.offset}`}
					className={tokenClass(token.type)}
				>
					{line.slice(token.offset, end)}
				</span>
			);
		});

		if (lineIndex < lines.length - 1) content.push("\n");
		return content;
	});
}

type CodePreProps = MarkdownComponentProps<"pre"> & {
	"data-code-title"?: string;
	"data-filename"?: string;
	"data-lang"?: string;
};

function MarkdownCodeBlock({ children, ...props }: CodePreProps) {
	const codeElement = Children.toArray(children).find(
		(child): child is ReactElement<{ children?: ReactNode }> =>
			isValidElement<{ children?: ReactNode }>(child),
	);
	const code = codeElement ? String(codeElement.props.children ?? "") : "";
	const language = props["data-lang"] ?? "plaintext";
	const label = props["data-filename"] ?? props["data-code-title"] ?? language;
	const [loadedLanguage, setLoadedLanguage] = useState<string | null>(null);
	const [copied, setCopied] = useState(false);
	const copyTimer = useRef<number | undefined>(undefined);

	useEffect(() => {
		let active = true;
		setLoadedLanguage(null);
		void loadMonacoLanguage(language).then((languageId) => {
			if (active) setLoadedLanguage(languageId);
		});
		return () => {
			active = false;
		};
	}, [language]);

	useEffect(
		() => () => {
			if (copyTimer.current !== undefined)
				window.clearTimeout(copyTimer.current);
		},
		[],
	);

	const renderedCode = useMemo(
		() => (loadedLanguage ? highlightedCode(code, loadedLanguage) : code),
		[code, loadedLanguage],
	);

	function copyCode() {
		void navigator.clipboard.writeText(code).then(() => {
			setCopied(true);
			if (copyTimer.current !== undefined)
				window.clearTimeout(copyTimer.current);
			copyTimer.current = window.setTimeout(() => setCopied(false), 1500);
		});
	}

	return (
		<div data-markdown-code data-language={language}>
			<div data-markdown-code-header>
				<span>{label}</span>
				<button
					type="button"
					onClick={copyCode}
					aria-label={copied ? "Code copied" : "Copy code"}
					title={copied ? "Copied" : "Copy code"}
				>
					{copied ? <Check aria-hidden="true" /> : <Copy aria-hidden="true" />}
				</button>
			</div>
			<pre {...props}>
				<code className={`language-${language}`}>{renderedCode}</code>
			</pre>
		</div>
	);
}

const components = {
	a({ href, children, ...props }: MarkdownComponentProps<"a">) {
		const internal = href?.startsWith("#");
		return (
			<a
				{...props}
				href={href}
				target={internal ? undefined : "_blank"}
				rel={internal ? undefined : "noopener noreferrer"}
				className="text-accent hover:text-accent-hover underline decoration-border-strong underline-offset-2"
			>
				{children}
			</a>
		);
	},
	h1({ children }: MarkdownComponentProps<"h1">) {
		return (
			<h1 className="mt-4 mb-2 font-semibold text-[15px] text-fg">
				{children}
			</h1>
		);
	},
	h2({ children }: MarkdownComponentProps<"h2">) {
		return (
			<h2 className="mt-3.5 mb-1.5 font-semibold text-[14px] text-fg">
				{children}
			</h2>
		);
	},
	h3({ children }: MarkdownComponentProps<"h3">) {
		return (
			<h3 className="mt-3 mb-1.5 font-semibold text-[13px] text-fg">
				{children}
			</h3>
		);
	},
	p({ children }: MarkdownComponentProps<"p">) {
		return (
			<p className="mb-2 last:mb-0 empty:hidden text-fg-muted">{children}</p>
		);
	},
	ul({ children }: MarkdownComponentProps<"ul">) {
		return <ul className="md-list md-ul text-fg-muted">{children}</ul>;
	},
	ol({ children, ...props }: MarkdownComponentProps<"ol">) {
		return (
			<ol {...props} className="md-list md-ol text-fg-muted">
				{children}
			</ol>
		);
	},
	li({ children }: MarkdownComponentProps<"li">) {
		return <li className="mb-0.5">{children}</li>;
	},
	blockquote({ children }: MarkdownComponentProps<"blockquote">) {
		return (
			<blockquote className="border-l-2 border-border pl-2.5 my-1.5 text-fg-dim">
				{children}
			</blockquote>
		);
	},
	strong({ children }: MarkdownComponentProps<"strong">) {
		return <strong className="font-semibold text-fg">{children}</strong>;
	},
	em({ children }: MarkdownComponentProps<"em">) {
		return <em className="text-fg-muted">{children}</em>;
	},
	del({ children }: MarkdownComponentProps<"del">) {
		return <del className="text-fg-dim">{children}</del>;
	},
	table({ children, ...props }: MarkdownComponentProps<"table">) {
		return (
			<div data-markdown-table>
				<table {...props}>{children}</table>
			</div>
		);
	},
	pre: MarkdownCodeBlock,
} satisfies MarkdownComponents;

export const MarkdownContent = memo(function MarkdownContent({
	text,
	streaming = false,
}: {
	text: string;
	streaming?: boolean;
}) {
	if (!text?.trim()) return null;

	return (
		<div data-markdown-content data-streaming={streaming || undefined}>
			<Markdown
				components={components}
				extensions={STREAMING_EXTENSIONS}
				frontmatter={false}
				headingIds={false}
			>
				{text}
			</Markdown>
		</div>
	);
});
