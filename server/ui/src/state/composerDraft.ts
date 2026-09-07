import type { PendingImage } from "../components/chat/images.ts";

export type ComposerContent = {
	text: string;
	files: string[];
	images: PendingImage[];
	editingQueueId: string | null;
};
type ComposerSnapshot = ComposerContent & {
	submitting: boolean;
	error: string | null;
};
const emptyContent = (): ComposerContent => ({
	text: "",
	files: [],
	images: [],
	editingQueueId: null,
});

// A tab owns its draft even while its chat panel is unmounted. Delivery state
// lives with the draft so navigation cannot resend it or lose a late receipt.
export class ComposerDraft {
	private value: ComposerSnapshot = {
		...emptyContent(),
		submitting: false,
		error: null,
	};
	private revision = 0;
	private listeners = new Set<() => void>();
	readonly getSnapshot = () => this.value;
	readonly subscribe = (listener: () => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};
	private publish(patch: Partial<ComposerSnapshot>) {
		this.value = { ...this.value, ...patch };
		for (const listener of this.listeners) listener();
	}
	readonly update = (patch: Partial<ComposerContent>) => {
		this.revision++;
		this.publish({ ...patch, error: null });
	};
	dismissError() {
		this.publish({ error: null });
	}
	async submit(
		send: (content: ComposerContent) => boolean | Promise<boolean>,
	): Promise<boolean> {
		if (this.value.submitting) return false;
		const content = this.value;
		const revision = this.revision;
		this.publish({ submitting: true, error: null });
		let sent = false;
		try {
			sent = await send(content);
		} catch {
			/* Keep the draft for retry. */
		}
		this.publish({
			...(sent && this.revision === revision ? emptyContent() : {}),
			submitting: false,
			error: sent
				? null
				: "Message was not accepted. Your draft has been kept.",
		});
		return sent;
	}
}
