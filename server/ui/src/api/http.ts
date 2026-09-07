export class APIError extends Error {
	readonly status: number;
	constructor(message: string, status: number) {
		super(message);
		this.name = "APIError";
		this.status = status;
	}
}

// Bind transport to an immutable server instance. Session clients never consult
// the current page singleton when sending an already accepted operation.
export function createWorkspaceHTTP(instanceId?: string) {
	const scopedFetch = (
		input: RequestInfo | URL,
		init?: RequestInit,
	): Promise<Response> => {
		const headers = new Headers(
			input instanceof Request ? input.headers : undefined,
		);
		new Headers(init?.headers).forEach((value, name) =>
			headers.set(name, value),
		);
		if (instanceId) headers.set("X-Wingman-Instance", instanceId);
		return fetch(input, { ...init, headers });
	};
	const checked = async (
		input: RequestInfo | URL,
		init?: RequestInit,
	): Promise<Response> => {
		const response = await scopedFetch(input, init);
		if (!response.ok) {
			const detail = (await response.text()).trim();
			throw new APIError(
				detail || `${response.status} ${response.statusText}`,
				response.status,
			);
		}
		return response;
	};
	return {
		scopedFetch,
		fetchOK: checked,
		fetchJSON: async <T>(
			input: RequestInfo | URL,
			init?: RequestInit,
		): Promise<T> => {
			return (await checked(input, init)).json() as Promise<T>;
		},
		workspaceURL: (path: string): string =>
			instanceId
				? `${path}${path.includes("?") ? "&" : "?"}instance=${encodeURIComponent(instanceId)}`
				: path,
	};
}

let http = createWorkspaceHTTP();
export function setWorkspaceHTTP(
	transport: ReturnType<typeof createWorkspaceHTTP>,
) {
	http = transport;
}
export function fetchJSON<T>(
	input: RequestInfo | URL,
	init?: RequestInit,
): Promise<T> {
	return http.fetchJSON<T>(input, init);
}
export function fetchOK(input: RequestInfo | URL, init?: RequestInit) {
	return http.fetchOK(input, init);
}
export function scopedFetch(input: RequestInfo | URL, init?: RequestInit) {
	return http.scopedFetch(input, init);
}
export function workspaceURL(path: string) {
	return http.workspaceURL(path);
}
