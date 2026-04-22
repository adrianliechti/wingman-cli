import { useEffect, useRef } from "react";
import Markdown from "react-markdown";

export function MarkdownContent({ text }: { text: string }) {
	if (!text) return null;

	return (
		<Markdown
			components={{
				code({ className, children }) {
					const match = /language-(\w+)/.exec(className || "");
					if (match) {
						return (
							<CodeBlock
								code={String(children).replace(/\n$/, "")}
								language={match[1]}
							/>
						);
					}
					return (
						<code className="bg-bg-surface px-1.5 py-0.5 rounded text-[11.5px]">
							{children}
						</code>
					);
				},
				pre({ children }) {
					return <>{children}</>;
				},
				a({ href, children }) {
					return (
						<a
							href={href}
							target="_blank"
							rel="noopener"
							className="text-accent hover:text-accent-hover"
						>
							{children}
						</a>
					);
				},
				h1({ children }) {
					return (
						<h1 className="mt-4 mb-2 font-semibold text-[15px] text-fg">
							{children}
						</h1>
					);
				},
				h2({ children }) {
					return (
						<h2 className="mt-3.5 mb-1.5 font-semibold text-[14px] text-fg">
							{children}
						</h2>
					);
				},
				h3({ children }) {
					return (
						<h3 className="mt-3 mb-1.5 font-semibold text-[13px] text-fg">
							{children}
						</h3>
					);
				},
				p({ children }) {
					return <p className="mb-2 text-fg-muted">{children}</p>;
				},
				ul({ children }) {
					return (
						<ul className="my-1.5 pl-5 list-disc text-fg-muted">{children}</ul>
					);
				},
				ol({ children }) {
					return (
						<ol className="my-1.5 pl-5 list-decimal text-fg-muted">
							{children}
						</ol>
					);
				},
				li({ children }) {
					return <li className="mb-0.5">{children}</li>;
				},
				blockquote({ children }) {
					return (
						<blockquote className="border-l-2 border-border pl-2.5 my-1.5 text-fg-dim">
							{children}
						</blockquote>
					);
				},
				strong({ children }) {
					return <strong className="font-semibold text-fg">{children}</strong>;
				},
				em({ children }) {
					return <em className="text-fg-muted">{children}</em>;
				},
			}}
		>
			{text}
		</Markdown>
	);
}

function CodeBlock({ code, language }: { code: string; language: string }) {
	const ref = useRef<HTMLPreElement>(null);

	useEffect(() => {
		let cancelled = false;
		import("monaco-editor").then((monaco) => {
			if (cancelled || !ref.current) return;

			try {
				monaco.editor.defineTheme("wingman", {
					base: "vs-dark",
					inherit: true,
					rules: [
						{ token: "comment", foreground: "555555" },
						{ token: "keyword", foreground: "a78bfa" },
						{ token: "string", foreground: "34d399" },
						{ token: "number", foreground: "fbbf24" },
						{ token: "type", foreground: "60a5fa" },
					],
					colors: {
						"editor.background": "#0a0a0a",
						"editor.foreground": "#e0e0e0",
					},
				});
			} catch {
				/* already defined */
			}
			monaco.editor.setTheme("wingman");

			monaco.editor
				.colorize(code, language || "plaintext", {})
				.then((html) => {
					if (!cancelled && ref.current) {
						ref.current.innerHTML = html;
					}
				})
				.catch(() => {});
		});
		return () => {
			cancelled = true;
		};
	}, [code, language]);

	return (
		<pre
			ref={ref}
			className="bg-bg-surface rounded-md my-2 px-3 py-2.5 overflow-x-auto text-[12px] leading-[1.55] font-mono text-fg-muted"
		>
			{code}
		</pre>
	);
}
