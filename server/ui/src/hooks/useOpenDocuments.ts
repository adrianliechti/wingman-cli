import { useCallback, useEffect, useRef, useState } from "react";
import { readWorkspaceFile, writeWorkspaceFile } from "../api/files";
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
	conflict?: boolean;
}

type Subscribe = (handler: (message: ServerMessage) => void) => () => void;

export function useOpenDocuments(subscribe?: Subscribe) {
	const [documents, setDocuments] = useState<Record<string, OpenDocument>>({});
	const documentsRef = useRef(documents);
	const requestRef = useRef<Record<string, number>>({});
	const updateDocuments = useCallback(
		(
			update: (
				current: Record<string, OpenDocument>,
			) => Record<string, OpenDocument>,
		) => {
			const current = documentsRef.current;
			const next = update(current);
			if (next === current) return;
			documentsRef.current = next;
			setDocuments(next);
		},
		[],
	);

	const readDocument = useCallback(
		async (path: string, external: boolean) => {
			const request = (requestRef.current[path] ?? 0) + 1;
			requestRef.current[path] = request;
			updateDocuments((current) => ({
				...current,
				[path]: {
					...(current[path] ?? emptyDocument(path, external)),
					loading: true,
					error: null,
				},
			}));
			try {
				const file = await readWorkspaceFile(path, external);
				if (requestRef.current[path] !== request) return;
				const content = file.content ?? "";
				updateDocuments((current) => ({
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
				updateDocuments((current) => ({
					...current,
					[path]: {
						...(current[path] ?? emptyDocument(path, external)),
						loading: false,
						error: error instanceof Error ? error.message : String(error),
					},
				}));
			}
		},
		[updateDocuments],
	);

	const openDocument = useCallback(
		(path: string, external = false) => {
			if (documentsRef.current[path]) return;
			const initial = emptyDocument(path, external);
			updateDocuments((current) => ({ ...current, [path]: initial }));
			void readDocument(path, external);
		},
		[readDocument, updateDocuments],
	);

	const openCreatedDocument = useCallback(
		(file: FileContent) => {
			const path = file.path;
			requestRef.current[path] = (requestRef.current[path] ?? 0) + 1;
			const content = file.content ?? "";
			updateDocuments((current) => ({
				...current,
				[path]: {
					path,
					external: false,
					file,
					draft: content,
					savedContent: content,
					loading: false,
					saving: false,
					error: null,
					saveError: null,
					conflict: false,
					revision: (current[path]?.revision ?? 0) + 1,
				},
			}));
		},
		[updateDocuments],
	);

	const updateDraft = useCallback(
		(path: string, draft: string) => {
			updateDocuments((current) => {
				const document = current[path];
				if (!document) return current;
				return {
					...current,
					[path]: { ...document, draft, saveError: null },
				};
			});
		},
		[updateDocuments],
	);

	const saveDocument = useCallback(
		async (path: string, force = false): Promise<SaveResult> => {
			const document = documentsRef.current[path];
			if (!document || document.external || document.file?.binary) {
				return { ok: true };
			}
			if (document.draft === document.savedContent) return { ok: true };
			if (document.conflict && !force) {
				return {
					ok: false,
					conflict: true,
					error: "This file changed on disk after it was opened.",
				};
			}
			updateDocuments((current) => ({
				...current,
				[path]: { ...current[path], saving: true, saveError: null },
			}));
			try {
				const result = await writeWorkspaceFile({
					path,
					content: document.draft,
					revision: document.file?.revision,
					force,
				});
				if (!result.ok) {
					updateDocuments((current) => {
						const latest = current[path];
						if (!latest) return current;
						return {
							...current,
							[path]: {
								...latest,
								saving: false,
								saveError: null,
								conflict: true,
							},
						};
					});
					return {
						ok: false,
						conflict: true,
						error: result.error,
					};
				}
				updateDocuments((current) => {
					const latest = current[path];
					if (!latest) return current;
					return {
						...current,
						[path]: {
							...latest,
							file: latest.file
								? {
										...latest.file,
										content: document.draft,
										revision: result.revision,
									}
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
				updateDocuments((current) => {
					const latest = current[path];
					if (!latest) return current;
					return {
						...current,
						[path]: {
							...latest,
							saving: false,
							saveError: message,
						},
					};
				});
				return { ok: false, error: message };
			}
		},
		[updateDocuments],
	);

	const discardDocument = useCallback(
		(path: string) => {
			updateDocuments((current) => {
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
		},
		[updateDocuments],
	);

	const closeDocument = useCallback(
		(path: string) => {
			requestRef.current[path] = (requestRef.current[path] ?? 0) + 1;
			updateDocuments((current) => {
				if (!current[path]) return current;
				const next = { ...current };
				delete next[path];
				return next;
			});
		},
		[updateDocuments],
	);

	const moveDocuments = useCallback(
		(from: string, to: string) => {
			updateDocuments((current) => {
				let changed = false;
				const next = { ...current };
				for (const [path, document] of Object.entries(current)) {
					if (path !== from && !path.startsWith(`${from}/`)) continue;
					const movedPath = `${to}${path.slice(from.length)}`;
					requestRef.current[path] = (requestRef.current[path] ?? 0) + 1;
					requestRef.current[movedPath] =
						(requestRef.current[movedPath] ?? 0) + 1;
					delete next[path];
					next[movedPath] = {
						...document,
						path: movedPath,
						file: document.file ? { ...document.file, path: movedPath } : null,
					};
					changed = true;
				}
				return changed ? next : current;
			});
		},
		[updateDocuments],
	);

	const refreshOpenDocuments = useCallback(async () => {
		const open = Object.values(documentsRef.current).filter(
			(document) =>
				!document.external &&
				!document.loading &&
				document.file &&
				!document.file.binary,
		);
		await Promise.all(
			open.map(async (document) => {
				const baseRevision = document.file?.revision;
				if (!baseRevision) return;
				const request = (requestRef.current[document.path] ?? 0) + 1;
				requestRef.current[document.path] = request;
				try {
					const file = await readWorkspaceFile(document.path);
					if (requestRef.current[document.path] !== request) return;
					const content = file.content ?? "";
					updateDocuments((current) => {
						const latest = current[document.path];
						if (!latest) return current;
						if (latest.file?.revision !== baseRevision) return current;
						if (file.revision === latest.file?.revision) {
							if (!latest.conflict) return current;
							return {
								...current,
								[document.path]: { ...latest, conflict: false },
							};
						}
						const dirty = latest.draft !== latest.savedContent;
						if (dirty) {
							if (latest.conflict) return current;
							return {
								...current,
								[document.path]: {
									...latest,
									conflict: true,
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
	}, [updateDocuments]);

	useEffect(() => {
		if (!subscribe) return;
		return subscribe((message) => {
			if (message.type === "files_changed") void refreshOpenDocuments();
		});
	}, [refreshOpenDocuments, subscribe]);

	useEffect(() => {
		const refresh = () => void refreshOpenDocuments();
		const interval = window.setInterval(refresh, 2_000);
		window.addEventListener("focus", refresh);
		return () => {
			window.clearInterval(interval);
			window.removeEventListener("focus", refresh);
		};
	}, [refreshOpenDocuments]);

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
		openCreatedDocument,
		updateDraft,
		saveDocument,
		discardDocument,
		reloadDocument: readDocument,
		closeDocument,
		moveDocuments,
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
