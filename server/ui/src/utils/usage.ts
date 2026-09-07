export function contextRemainingPercent(
	lastInputTokens: number,
	contextWindow: number,
): number | null {
	if (contextWindow <= 0 || lastInputTokens <= 0) return null;
	const used = Math.min(lastInputTokens, contextWindow);
	return Math.max(0, 100 - Math.round((used / contextWindow) * 100));
}

export function shouldShowContextIndicator(
	lastInputTokens: number,
	contextWindow: number,
): boolean {
	const remaining = contextRemainingPercent(lastInputTokens, contextWindow);
	return remaining !== null && remaining < 25;
}
