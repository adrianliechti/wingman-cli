import { code } from "@streamdown/code";
import { math } from "@streamdown/math";
import { mermaid } from "@streamdown/mermaid";
import { memo } from "react";
import { type BundledTheme, Streamdown } from "streamdown";
import "katex/dist/katex.min.css";

// Streamdown selects the dark Shiki tokens from the root `.dark` class, which
// the system color-scheme listener keeps aligned with the OS preference.
const SHIKI_THEME: [BundledTheme, BundledTheme] = [
	"github-light",
	"github-dark",
];

// Code highlighting (Shiki), mermaid diagrams, and KaTeX math. Built-in copy/
// download controls come on by default and are suppressed while streaming.
const PLUGINS = { code, mermaid, math };

// Prose elements styled to match the app's compact terminal theme. Code blocks,
// tables, mermaid and math are left to Streamdown's own (interactive) renderers.
const components = {
	a({ href, children }: { href?: string; children?: React.ReactNode }) {
		return (
			<a
				href={href}
				target="_blank"
				rel="noopener noreferrer"
				className="text-accent hover:text-accent-hover"
			>
				{children}
			</a>
		);
	},
	h1({ children }: { children?: React.ReactNode }) {
		return (
			<h1 className="mt-4 mb-2 font-semibold text-[15px] text-fg">
				{children}
			</h1>
		);
	},
	h2({ children }: { children?: React.ReactNode }) {
		return (
			<h2 className="mt-3.5 mb-1.5 font-semibold text-[14px] text-fg">
				{children}
			</h2>
		);
	},
	h3({ children }: { children?: React.ReactNode }) {
		return (
			<h3 className="mt-3 mb-1.5 font-semibold text-[13px] text-fg">
				{children}
			</h3>
		);
	},
	p({ children }: { children?: React.ReactNode }) {
		return (
			<p className="mb-2 last:mb-0 empty:hidden text-fg-muted">{children}</p>
		);
	},
	ul({ children }: { children?: React.ReactNode }) {
		return <ul className="md-list md-ul text-fg-muted">{children}</ul>;
	},
	ol({ children }: { children?: React.ReactNode }) {
		return <ol className="md-list md-ol text-fg-muted">{children}</ol>;
	},
	li({ children }: { children?: React.ReactNode }) {
		return <li className="mb-0.5">{children}</li>;
	},
	blockquote({ children }: { children?: React.ReactNode }) {
		return (
			<blockquote className="border-l-2 border-border pl-2.5 my-1.5 text-fg-dim">
				{children}
			</blockquote>
		);
	},
	strong({ children }: { children?: React.ReactNode }) {
		return <strong className="font-semibold text-fg">{children}</strong>;
	},
	em({ children }: { children?: React.ReactNode }) {
		return <em className="text-fg-muted">{children}</em>;
	},
	del({ children }: { children?: React.ReactNode }) {
		return <del className="text-fg-dim">{children}</del>;
	},
};

export const MarkdownContent = memo(function MarkdownContent({
	text,
	streaming = false,
}: {
	text: string;
	streaming?: boolean;
}) {
	const trimmed = text?.replace(/\s+$/, "");
	if (!trimmed) return null;

	return (
		<Streamdown
			shikiTheme={SHIKI_THEME}
			plugins={PLUGINS}
			isAnimating={streaming}
			lineNumbers={false}
			components={components}
		>
			{trimmed}
		</Streamdown>
	);
});
