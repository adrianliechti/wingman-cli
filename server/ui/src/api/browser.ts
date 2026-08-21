export interface BrowserProvider {
	id: "chrome" | "safari";
	name: string;
	available: boolean;
	connected: boolean;
	configured: boolean;
	requires_download?: boolean;
	description: string;
	setup?: string;
	server?: string;
}

export interface BrowserStatus {
	providers: BrowserProvider[];
	selected: string;
}

async function responseError(response: Response): Promise<Error> {
	const detail = (await response.text()).trim();
	return new Error(detail || `Browser request failed (${response.status})`);
}

export async function getBrowserStatus(): Promise<BrowserStatus> {
	const response = await fetch("/api/browser/");
	if (!response.ok) throw await responseError(response);
	return response.json();
}

export async function connectBrowser(provider: string): Promise<void> {
	const response = await fetch("/api/browser/connect", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ provider }),
	});
	if (!response.ok) throw await responseError(response);
}

export async function selectBrowser(provider: string): Promise<void> {
	const response = await fetch("/api/browser/select", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ provider }),
	});
	if (!response.ok) throw await responseError(response);
}

export async function openBrowserPage(
	provider: string,
	url: string,
): Promise<string> {
	const response = await fetch("/api/browser/open", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ provider, url }),
	});
	if (!response.ok) throw await responseError(response);
	const data = (await response.json()) as { result?: string };
	return data.result ?? "";
}

export async function getBrowserSnapshot(provider: string): Promise<string> {
	const response = await fetch(
		`/api/browser/snapshot?provider=${encodeURIComponent(provider)}`,
	);
	if (!response.ok) throw await responseError(response);
	const data = (await response.json()) as { snapshot?: string };
	return data.snapshot ?? "";
}

export async function getBrowserPageInfo(provider: string): Promise<string> {
	const response = await fetch(
		`/api/browser/page?provider=${encodeURIComponent(provider)}`,
	);
	if (!response.ok) throw await responseError(response);
	const data = (await response.json()) as { info?: string };
	return data.info ?? "";
}

export async function getBrowserScreenshot(
	provider: string,
	elementUID?: string,
): Promise<Blob> {
	const uid = elementUID ? `&uid=${encodeURIComponent(elementUID)}` : "";
	const response = await fetch(
		`/api/browser/screenshot?provider=${encodeURIComponent(provider)}${uid}`,
	);
	if (!response.ok) throw await responseError(response);
	return response.blob();
}
