export type EditorSaveParticipant = () => Promise<boolean | void>;

const participants = new Map<string, Set<EditorSaveParticipant>>();
const pendingRuns = new Map<string, Promise<boolean>>();

// Editor integrations register save-time source actions here so persistence
// stays language-neutral and a missing language server never blocks saving.
export function registerEditorSaveParticipant(
	path: string,
	participant: EditorSaveParticipant,
): () => void {
	let registered = participants.get(path);
	if (!registered) {
		registered = new Set();
		participants.set(path, registered);
	}
	registered.add(participant);

	return () => {
		const current = participants.get(path);
		if (!current) return;
		current.delete(participant);
		if (current.size === 0) participants.delete(path);
	};
}

export function runEditorSaveParticipants(path: string): Promise<boolean> {
	const pending = pendingRuns.get(path);
	if (pending) return pending;

	const run = (async () => {
		let changed = false;
		for (const participant of [...(participants.get(path) ?? [])]) {
			try {
				changed = (await participant()) === true || changed;
			} catch {
				// Saving the user's buffer is more important than a failed optional
				// source action.
			}
		}
		return changed;
	})();
	pendingRuns.set(path, run);
	void run.finally(() => {
		if (pendingRuns.get(path) === run) pendingRuns.delete(path);
	});
	return run;
}
