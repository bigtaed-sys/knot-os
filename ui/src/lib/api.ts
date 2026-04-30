// Thin REST client for the knotd API.
// Replaced in M2 with typed endpoints generated from the backend contract.

export const API_BASE = '/api';

export class ApiError extends Error {
	constructor(
		message: string,
		public readonly status: number,
		public readonly body?: unknown
	) {
		super(message);
		this.name = 'ApiError';
	}
}

export async function apiGet<T>(path: string): Promise<T> {
	const res = await fetch(`${API_BASE}${path}`, { credentials: 'same-origin' });
	if (!res.ok) {
		throw new ApiError(`GET ${path} → ${res.status}`, res.status, await safeJson(res));
	}
	return res.json() as Promise<T>;
}

export async function apiPost<T>(path: string, body?: unknown): Promise<T> {
	const res = await fetch(`${API_BASE}${path}`, {
		method: 'POST',
		credentials: 'same-origin',
		headers: { 'content-type': 'application/json' },
		body: body === undefined ? undefined : JSON.stringify(body)
	});
	if (!res.ok) {
		throw new ApiError(`POST ${path} → ${res.status}`, res.status, await safeJson(res));
	}
	return res.json() as Promise<T>;
}

async function safeJson(res: Response): Promise<unknown> {
	try {
		return await res.json();
	} catch {
		return undefined;
	}
}
