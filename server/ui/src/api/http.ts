export class APIError extends Error {
	readonly status: number;

	constructor(message: string, status: number) {
		super(message);
		this.name = "APIError";
		this.status = status;
	}
}

async function checkedResponse(
	input: RequestInfo | URL,
	init?: RequestInit,
): Promise<Response> {
	const response = await fetch(input, init);
	if (!response.ok) {
		const detail = (await response.text()).trim();
		throw new APIError(
			detail || `${response.status} ${response.statusText}`,
			response.status,
		);
	}
	return response;
}

export async function fetchJSON<T>(
	input: RequestInfo | URL,
	init?: RequestInit,
): Promise<T> {
	const response = await checkedResponse(input, init);
	return (await response.json()) as T;
}

export function fetchOK(
	input: RequestInfo | URL,
	init?: RequestInit,
): Promise<Response> {
	return checkedResponse(input, init);
}
