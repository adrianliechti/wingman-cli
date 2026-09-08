import { useCallback, useEffect, useRef, useState } from "react";
import { readWorkspaceFile, writeWorkspaceFile } from "../api/files";
import { runEditorSaveParticipants } from "../editorSaveParticipants";
import { syncLSPDocument, type LSPDocumentEvent } from "../api/lsp";
import type { FileContent, ServerMessage } from "../types/protocol";
import {
	applyTextEdits,
	contentRevision,
	textEditOperations,
	type WorkspaceEditEnvelope,
} from "../workspaceEdit";

export interface OpenDocument {
	path: string;
	external: boolean;
	untitled: boolean;
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
	dirty?: boolean;
	error?: string;
	conflict?: boolean;
}

export interface ApplyWorkspaceEditResult extends SaveResult {
	paths?: string[];
}

type Subscribe = (handler: (message: ServerMessage) => void) => () => void;

export function useOpenDocuments(subscribe?: Subscribe) {
	const [documents, setDocuments] = useState<Record<string, OpenDocument>>({});
	const documentsRef = useRef(documents);
	const requestRef = useRef<Record<string, number>>({});
	const savesRef = useRef(new Map<string, { file: FileContent | null; promise: Promise<SaveResult> }>());
	const lspQueuesRef = useRef(new Map<string, Promise<void>>());
	const lspChangeTimersRef = useRef(
		new Map<string, { timer: number; content: string }>(),
	);
	const queueLSPEvent = useCallback(
		(event: LSPDocumentEvent, path: string, content = "") => {
			const previous = lspQueuesRef.current.get(path) ?? Promise.resolve();
			const next = previous
				.then(() => syncLSPDocument(event, path, content))
				.catch(() => {
					// Feature requests still synchronize their current buffer, so a
					// transient lifecycle failure is recoverable and should not block editing.
				});
			lspQueuesRef.current.set(path, next);
			void next.finally(() => {
				if (lspQueuesRef.current.get(path) === next) {
					lspQueuesRef.current.delete(path);
				}
			});
		},
		[],
	);
	const cancelPendingLSPChange = useCallback((path: string) => {
		const pending = lspChangeTimersRef.current.get(path);
		if (!pending) return;
		window.clearTimeout(pending.timer);
		lspChangeTimersRef.current.delete(path);
	}, []);
	const scheduleLSPChange = useCallback(
		(path: string, content: string) => {
			cancelPendingLSPChange(path);
			const timer = window.setTimeout(() => {
				lspChangeTimersRef.current.delete(path);
				queueLSPEvent("change", path, content);
			}, 150);
			lspChangeTimersRef.current.set(path, { timer, content });
		},
		[cancelPendingLSPChange, queueLSPEvent],
	);
	const flushLSPChange = useCallback(
		(path: string) => {
			const pending = lspChangeTimersRef.current.get(path);
			if (!pending) return;
			cancelPendingLSPChange(path);
			queueLSPEvent("change", path, pending.content);
		},
		[cancelPendingLSPChange, queueLSPEvent],
	);
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
					saving: false,
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
				if (!external && !file.binary) {
					queueLSPEvent("open", path, content);
				}
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
		[queueLSPEvent, updateDocuments],
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
					untitled: false,
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
			if (!file.binary) queueLSPEvent("open", path, content);
		},
		[queueLSPEvent, updateDocuments],
	);

	const openUntitledDocument = useCallback(
		(path: string) => {
			const file: FileContent = {
				path,
				content: "",
				language: "plaintext",
				revision: "",
				size: 0,
			};
			requestRef.current[path] = (requestRef.current[path] ?? 0) + 1;
			updateDocuments((current) => ({
				...current,
				[path]: {
					path,
					external: false,
					untitled: true,
					file,
					draft: "",
					savedContent: "",
					loading: false,
					saving: false,
					error: null,
					saveError: null,
					conflict: false,
					revision: 0,
				},
			}));
		},
		[updateDocuments],
	);

	const updateDraft = useCallback(
		(path: string, draft: string) => {
			const document = documentsRef.current[path];
			if (
				document &&
				!document.external &&
				!document.untitled &&
				!document.file?.binary
			) {
				scheduleLSPChange(path, draft);
			}
			updateDocuments((current) => {
				const document = current[path];
				if (!document) return current;
				return {
					...current,
					[path]: { ...document, draft, saveError: null },
				};
			});
		},
		[scheduleLSPChange, updateDocuments],
	);

	const performSaveDocument = useCallback(
		async (path: string, force = false): Promise<SaveResult> => {
			let document = documentsRef.current[path];
			if (!document || document.external || document.file?.binary) {
				return { ok: true };
			}
			if (document.untitled) {
				return { ok: false, error: "Choose a name before saving this file." };
			}
			const file = document.file;
			flushLSPChange(path);
			// Saving a clean buffer is a no-op; save participants must not get a
			// chance to rewrite an unmodified file on disk.
			if (document.draft === document.savedContent) {
				queueLSPEvent("save", path, document.draft);
				return { ok: true };
			}
			updateDocuments((current) => ({
				...current,
				[path]: { ...current[path], saving: true, saveError: null },
			}));
			try {
				await runEditorSaveParticipants(path);
				document = documentsRef.current[path];
				if (!document || document.file !== file) {
					return { ok: false, error: "The file was closed, moved, or reloaded while saving." };
				}
				// Save participants may change the buffer. Persist their newest edits.
				flushLSPChange(path);
				if (document.draft === document.savedContent) {
					queueLSPEvent("save", path, document.draft);
					return { ok: true };
				}
				if (document.conflict && !force) {
					return { ok: false, conflict: true, error: "This file changed on disk after it was opened." };
				}
				const result = await writeWorkspaceFile({
					path,
					content: document.draft,
					revision: document.file?.revision,
					force,
				});
				if (documentsRef.current[path]?.file !== file) {
					return { ok: false, error: "The file was closed, moved, or reloaded while saving." };
				}
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
				const latest = documentsRef.current[path];
				const dirty = latest.draft !== document.draft;
				cancelPendingLSPChange(path);
				queueLSPEvent("save", path, document.draft);
				if (dirty) queueLSPEvent("change", path, latest.draft);
				return { ok: true, dirty };
			} catch (error) {
				const message = error instanceof Error ? error.message : String(error);
				updateDocuments((current) => {
					const latest = current[path];
					if (!latest || latest.file !== file) return current;
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
			} finally {
				updateDocuments((current) => {
					const latest = current[path];
					if (!latest?.saving || latest.file !== file) return current;
					return { ...current, [path]: { ...latest, saving: false } };
				});
			}
		},
		[cancelPendingLSPChange, flushLSPChange, queueLSPEvent, updateDocuments],
	);

	const saveDocument = useCallback(
		(path: string, force = false): Promise<SaveResult> => {
			const file = documentsRef.current[path]?.file ?? null;
			const pending = savesRef.current.get(path);
			if (pending?.file === file) return pending.promise;
			const promise = performSaveDocument(path, force);
			savesRef.current.set(path, { file, promise });
			void promise.finally(() => {
				if (savesRef.current.get(path)?.promise === promise) savesRef.current.delete(path);
			});
			return promise;
		},
		[performSaveDocument],
	);

	const discardDocument = useCallback(
		(path: string) => {
			const document = documentsRef.current[path];
			if (
				document &&
				!document.external &&
				!document.untitled &&
				!document.file?.binary
			) {
				cancelPendingLSPChange(path);
				queueLSPEvent("save", path, document.savedContent);
			}
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
		[cancelPendingLSPChange, queueLSPEvent, updateDocuments],
	);

	const applyWorkspaceEdit = useCallback(
		async (
			envelope: WorkspaceEditEnvelope,
		): Promise<ApplyWorkspaceEditResult> => {
			try {
				const operations = textEditOperations(envelope);
				if (operations.size === 0) return { ok: true, paths: [] };
				const prepared: Array<{
					path: string;
					content: string;
					original: string;
					file: FileContent;
					base?: OpenDocument;
				}> = [];
				const paths = new Set<string>();

				for (const [documentUri, groups] of operations) {
					const expected = envelope.documents[documentUri];
					if (!expected?.revision) throw new Error("Missing file revision.");
					if (paths.has(expected.path)) {
						throw new Error(
							`The language server addressed “${expected.path}” more than once.`,
						);
					}
					paths.add(expected.path);
					const open = documentsRef.current[expected.path];
					if (open?.external || open?.file?.binary) {
						throw new Error(`Cannot edit read-only file “${expected.path}”.`);
					}
					if (open && (!open.file || open.loading)) {
						throw new Error(`“${expected.path}” is still loading.`);
					}
					if (
						open &&
						(await contentRevision(open.draft)) !== expected.revision
					) {
						throw new Error(
							`“${expected.path}” changed after the language server prepared this edit. Try again.`,
						);
					}
					let file: FileContent;
					let content: string;
					if (open?.file) {
						if (open.conflict) {
							throw new Error(`“${expected.path}” changed on disk.`);
						}
						file = open.file;
						content = open.draft;
					} else {
						const current = await readWorkspaceFile(expected.path);
						if (current.binary || current.revision !== expected.revision) {
							throw new Error(
								`“${expected.path}” changed before the edit was applied.`,
							);
						}
						file = current;
						content = current.content ?? "";
					}

					const original = content;
					for (const group of groups)
						content = applyTextEdits(content, group.edits);
					prepared.push({
						path: expected.path,
						content,
						original,
						file,
						base: open,
					});
				}

				// Do not partially apply a multi-file edit if any target changed while
				// unopened files were being read.
				for (const target of prepared) {
					if (documentsRef.current[target.path] !== target.base) {
						throw new Error(
							`“${target.path}” changed while the language server edit was prepared. Try again.`,
						);
					}
				}

				const changed = prepared.filter(
					(target) => target.content !== target.original,
				);
				if (changed.length === 0) return { ok: true, paths: [] };
				updateDocuments((current) => {
					const next = { ...current };
					for (const target of changed) {
						const base = target.base;
						next[target.path] = {
							...(base ?? {
								path: target.path,
								external: false,
								untitled: false,
								file: target.file,
								draft: target.original,
								savedContent: target.original,
								loading: false,
								saving: false,
								error: null,
								saveError: null,
								conflict: false,
								revision: 0,
							}),
							draft: target.content,
							conflict: false,
							saveError: null,
							revision: (base?.revision ?? 0) + 1,
						};
					}
					return next;
				});
				for (const target of changed) {
					cancelPendingLSPChange(target.path);
					// "change" also opens a document that was not editor-owned yet,
					// while correctly preserving its unsaved state in the LSP session.
					queueLSPEvent("change", target.path, target.content);
				}
				return { ok: true, paths: changed.map((target) => target.path) };
			} catch (error) {
				return {
					ok: false,
					error: error instanceof Error ? error.message : String(error),
				};
			}
		},
		[cancelPendingLSPChange, queueLSPEvent, updateDocuments],
	);

	const closeDocument = useCallback(
		(path: string) => {
			const document = documentsRef.current[path];
			cancelPendingLSPChange(path);
			if (
				document &&
				!document.external &&
				!document.untitled &&
				!document.file?.binary
			) {
				queueLSPEvent("close", path);
			}
			requestRef.current[path] = (requestRef.current[path] ?? 0) + 1;
			updateDocuments((current) => {
				if (!current[path]) return current;
				const next = { ...current };
				delete next[path];
				return next;
			});
		},
		[cancelPendingLSPChange, queueLSPEvent, updateDocuments],
	);

	const moveDocuments = useCallback(
		(from: string, to: string) => {
			for (const [path, document] of Object.entries(documentsRef.current)) {
				if (path !== from && !path.startsWith(`${from}/`)) continue;
				if (document.external || document.file?.binary) continue;
				const movedPath = `${to}${path.slice(from.length)}`;
				cancelPendingLSPChange(path);
				queueLSPEvent("close", path);
				if (document.loading) continue;
				queueLSPEvent(
					document.draft === document.savedContent ? "open" : "change",
					movedPath,
					document.draft,
				);
			}
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
						saving: false,
						file: document.file ? { ...document.file, path: movedPath } : null,
					};
					changed = true;
				}
				return changed ? next : current;
			});
			for (const document of Object.values(documentsRef.current)) {
				if (
					document.loading &&
					(document.path === to || document.path.startsWith(`${to}/`))
				) {
					void readDocument(document.path, document.external);
				}
			}
		},
		[cancelPendingLSPChange, queueLSPEvent, readDocument, updateDocuments],
	);

	const refreshOpenDocuments = useCallback(async () => {
		const open = Object.values(documentsRef.current).filter(
			(document) =>
				!document.external &&
				!document.loading &&
				!document.saving &&
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
					let synchronize = false;
					updateDocuments((current) => {
						const latest = current[document.path];
						if (!latest || latest.saving) return current;
						if (latest.file?.revision !== baseRevision) return current;
						if (file.revision === latest.file?.revision) {
							synchronize = latest.draft === latest.savedContent;
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
						synchronize = true;
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
					if (synchronize) queueLSPEvent("save", document.path, content);
				} catch {}
			}),
		);
	}, [queueLSPEvent, updateDocuments]);

	useEffect(() => {
		const pendingChanges = lspChangeTimersRef.current;
		return () => {
			for (const pending of pendingChanges.values()) {
				window.clearTimeout(pending.timer);
			}
			pendingChanges.clear();
		};
	}, []);

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
					(document.untitled || document.draft !== document.savedContent),
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
		openUntitledDocument,
		updateDraft,
		saveDocument,
		discardDocument,
		reloadDocument: readDocument,
		closeDocument,
		moveDocuments,
		applyWorkspaceEdit,
	};
}

function emptyDocument(path: string, external: boolean): OpenDocument {
	return {
		path,
		external,
		untitled: false,
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
