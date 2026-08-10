import { useCallback, useEffect, useRef, useState } from "react";
import type { FileContent, ServerMessage } from "../types/protocol";

export interface OpenDocument {
	path: string;
	external: boolean;
	file: FileContent | null;
	draft: string;
	savedContent: string;
	loading: boolean;
	saving: boolean;
	error: string | null;
	saveError: string | null;
	conflict: boolean;
	revision: number;
}

export interface SaveResult {
	ok: boolean;
	error?: string;
}

type Subscribe = (handler: (message: ServerMessage) => void) => () => void;

export function useOpenDocuments(subscribe?: Subscribe) {
	const [documents, setDocuments] = useState<Record<string, OpenDocument>>({});
	const documentsRef = useRef(documents);
	const requestRef = useRef<Record<string, number>>({});
	useEffect(() => {
		documentsRef.current = documents;
	}, [documents]);

	const readDocument = useCallback(async (path: string, external: boolean) => {
		const request = (requestRef.current[path] ?? 0) + 1;
		requestRef.current[path] = request;
		setDocuments((current) => ({
			...current,
			[path]: {
				...(current[path] ?? emptyDocument(path, external)),
				loading: true,
				error: null,
			},
		}));
		try {
			const response = await fetch(
				`${external ? "/api/lsp/file" : "/api/files/read"}?path=${encodeURIComponent(path)}`,
			);
			if (!response.ok) {
				throw new Error(
					(await response.text()).trim() ||
						`Failed to load file (${response.status}).`,
				);
			}
			const file = (await response.json()) as FileContent;
			if (requestRef.current[path] !== request) return;
			const content = file.content ?? "";
			setDocuments((current) => ({
				...current,
				[path]: {
					...(current[path] ?? emptyDocument(path, external)),
					file,
					draft: content,
					savedContent: content,
					loading: false,
					error: null,
					saveError: null,
					conflict: false,
					revision: (current[path]?.revision ?? 0) + 1,
				},
			}));
		} catch (error) {
			if (requestRef.current[path] !== request) return;
			setDocuments((current) => ({
				...current,
				[path]: {
					...(current[path] ?? emptyDocument(path, external)),
					loading: false,
					error: error instanceof Error ? error.message : String(error),
				},
			}));
		}
	}, []);

	const openDocument = useCallback(
		(path: string, external = false) => {
			if (documentsRef.current[path]) return;
			const initial = emptyDocument(path, external);
			documentsRef.current = { ...documentsRef.current, [path]: initial };
			setDocuments((current) => ({ ...current, [path]: initial }));
			void readDocument(path, external);
		},
		[readDocument],
	);

	const updateDraft = useCallback((path: string, draft: string) => {
		const currentDocument = documentsRef.current[path];
		if (currentDocument) {
			documentsRef.current = {
				...documentsRef.current,
				[path]: { ...currentDocument, draft, saveError: null },
			};
		}
		setDocuments((current) => {
			const document = current[path];
			if (!document) return current;
			return {
				...current,
				[path]: { ...document, draft, saveError: null },
			};
		});
	}, []);

	const saveDocument = useCallback(
		async (path: string): Promise<SaveResult> => {
			const document = documentsRef.current[path];
			if (!document || document.external || document.file?.binary) {
				return { ok: true };
			}
			if (document.draft === document.savedContent) return { ok: true };
			setDocuments((current) => ({
				...current,
				[path]: { ...current[path], saving: true, saveError: null },
			}));
			try {
				const response = await fetch("/api/files/write", {
					method: "POST",
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify({ path, content: document.draft }),
				});
				if (!response.ok) {
					throw new Error(
						(await response.text()).trim() ||
							`Failed to save file (${response.status}).`,
					);
				}
				setDocuments((current) => {
					const latest = current[path];
					if (!latest) return current;
					return {
						...current,
						[path]: {
							...latest,
							file: latest.file
								? { ...latest.file, content: document.draft }
								: latest.file,
							savedContent: document.draft,
							saving: false,
							saveError: null,
							conflict: false,
						},
					};
				});
				return { ok: true };
			} catch (error) {
				const message = error instanceof Error ? error.message : String(error);
				setDocuments((current) => ({
					...current,
					[path]: {
						...current[path],
						saving: false,
						saveError: message,
					},
				}));
				return { ok: false, error: message };
			}
		},
		[],
	);

	const discardDocument = useCallback((path: string) => {
		setDocuments((current) => {
			const document = current[path];
			if (!document) return current;
			return {
				...current,
				[path]: {
					...document,
					draft: document.savedContent,
					saveError: null,
					conflict: false,
				},
			};
		});
	}, []);

	const keepDocument = useCallback((path: string) => {
		setDocuments((current) => ({
			...current,
			[path]: { ...current[path], conflict: false },
		}));
	}, []);

	const closeDocument = useCallback((path: string) => {
		requestRef.current[path] = (requestRef.current[path] ?? 0) + 1;
		if (documentsRef.current[path]) {
			const next = { ...documentsRef.current };
			delete next[path];
			documentsRef.current = next;
		}
		setDocuments((current) => {
			if (!current[path]) return current;
			const next = { ...current };
			delete next[path];
			return next;
		});
	}, []);

	const refreshOpenDocuments = useCallback(async () => {
		const open = Object.values(documentsRef.current).filter(
			(document) => !document.external && document.file && !document.loading,
		);
		await Promise.all(
			open.map(async (document) => {
				try {
					const response = await fetch(
						`/api/files/read?path=${encodeURIComponent(document.path)}`,
					);
					if (!response.ok) return;
					const file = (await response.json()) as FileContent;
					const content = file.content ?? "";
					setDocuments((current) => {
						const latest = current[document.path];
						if (!latest) return current;
						const dirty = latest.draft !== latest.savedContent;
						if (dirty) {
							if (content === latest.savedContent) return current;
							return {
								...current,
								[document.path]: {
									...latest,
									conflict: true,
									revision: latest.revision + 1,
								},
							};
						}
						return {
							...current,
							[document.path]: {
								...latest,
								file,
								draft: content,
								savedContent: content,
								conflict: false,
								revision: latest.revision + 1,
							},
						};
					});
				} catch {}
			}),
		);
	}, []);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			if (message.type === "files_changed") void refreshOpenDocuments();
		});
	}, [refreshOpenDocuments, subscribe]);

	const dirtyPaths = new Set(
		Object.values(documents)
			.filter(
				(document) =>
					!document.external &&
					!document.file?.binary &&
					document.draft !== document.savedContent,
			)
			.map((document) => document.path),
	);

	useEffect(() => {
		if (dirtyPaths.size === 0) return;
		const protect = (event: BeforeUnloadEvent) => {
			event.preventDefault();
			event.returnValue = true;
		};
		window.addEventListener("beforeunload", protect);
		return () => window.removeEventListener("beforeunload", protect);
	}, [dirtyPaths.size]);

	return {
		documents,
		dirtyPaths,
		openDocument,
		updateDraft,
		saveDocument,
		discardDocument,
		keepDocument,
		reloadDocument: readDocument,
		closeDocument,
	};
}

function emptyDocument(path: string, external: boolean): OpenDocument {
	return {
		path,
		external,
		file: null,
		draft: "",
		savedContent: "",
		loading: true,
		saving: false,
		error: null,
		saveError: null,
		conflict: false,
		revision: 0,
	};
}
