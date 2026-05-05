// Thin REST client for the knotd API.
// Replaced in M2 with typed endpoints generated from the backend contract.

export const API_BASE = '/api';

// Default per-request timeout. Browser fetch has no built-in timeout
// — without this a backend that wedges on a single endpoint produces
// indefinitely-spinning UI panels, which is what bit /setup/capability
// once and is annoying enough to warrant a default.
const DEFAULT_TIMEOUT_MS = 15000;

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

export class ApiTimeoutError extends Error {
	constructor(public readonly path: string, public readonly ms: number) {
		super(`request to ${path} timed out after ${ms}ms`);
		this.name = 'ApiTimeoutError';
	}
}

interface RequestOpts {
	timeoutMs?: number; // 0 disables the timeout
}

function abortAfter(ms: number): { signal: AbortSignal; cancel: () => void } {
	const ctrl = new AbortController();
	if (ms <= 0) return { signal: ctrl.signal, cancel: () => {} };
	const timer = setTimeout(() => ctrl.abort(), ms);
	return { signal: ctrl.signal, cancel: () => clearTimeout(timer) };
}

async function doFetch(path: string, init: RequestInit, opts: RequestOpts): Promise<Response> {
	const ms = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS;
	const { signal, cancel } = abortAfter(ms);
	try {
		return await fetch(`${API_BASE}${path}`, { ...init, signal });
	} catch (e) {
		// AbortError surfaces as a DOMException whose name === 'AbortError'.
		// Re-wrap so callers can branch on instanceof ApiTimeoutError without
		// cross-realm pitfalls.
		if (e instanceof DOMException && e.name === 'AbortError') {
			throw new ApiTimeoutError(path, ms);
		}
		throw e;
	} finally {
		cancel();
	}
}

export async function apiGet<T>(path: string, opts: RequestOpts = {}): Promise<T> {
	const res = await doFetch(path, { credentials: 'same-origin' }, opts);
	if (!res.ok) {
		throw new ApiError(`GET ${path} → ${res.status}`, res.status, await safeJson(res));
	}
	return res.json() as Promise<T>;
}

export async function apiPost<T>(path: string, body?: unknown, opts: RequestOpts = {}): Promise<T> {
	const res = await doFetch(
		path,
		{
			method: 'POST',
			credentials: 'same-origin',
			headers: { 'content-type': 'application/json' },
			body: body === undefined ? undefined : JSON.stringify(body)
		},
		opts
	);
	if (!res.ok) {
		throw new ApiError(`POST ${path} → ${res.status}`, res.status, await safeJson(res));
	}
	return res.json() as Promise<T>;
}

export async function apiPut<T>(path: string, body?: unknown, opts: RequestOpts = {}): Promise<T> {
	const res = await doFetch(
		path,
		{
			method: 'PUT',
			credentials: 'same-origin',
			headers: { 'content-type': 'application/json' },
			body: body === undefined ? undefined : JSON.stringify(body)
		},
		opts
	);
	if (!res.ok) {
		throw new ApiError(`PUT ${path} → ${res.status}`, res.status, await safeJson(res));
	}
	return res.json() as Promise<T>;
}

export async function apiPatch<T>(path: string, body?: unknown, opts: RequestOpts = {}): Promise<T> {
	const res = await doFetch(
		path,
		{
			method: 'PATCH',
			credentials: 'same-origin',
			headers: { 'content-type': 'application/json' },
			body: body === undefined ? undefined : JSON.stringify(body)
		},
		opts
	);
	if (!res.ok) {
		throw new ApiError(`PATCH ${path} → ${res.status}`, res.status, await safeJson(res));
	}
	return res.json() as Promise<T>;
}

export async function apiDelete<T = unknown>(path: string, opts: RequestOpts = {}): Promise<T> {
	const res = await doFetch(
		path,
		{ method: 'DELETE', credentials: 'same-origin' },
		opts
	);
	if (!res.ok) {
		throw new ApiError(`DELETE ${path} → ${res.status}`, res.status, await safeJson(res));
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
