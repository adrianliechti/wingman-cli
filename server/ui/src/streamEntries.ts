export function discardUncommittedStreamEntries<
	T extends { id: string; toolPartial?: boolean },
>(entries: T[], attemptIds?: ReadonlySet<string>): T[] {
	return entries.filter(
		(entry) => !entry.toolPartial && !attemptIds?.has(entry.id),
	);
}
