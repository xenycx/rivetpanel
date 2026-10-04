export class ApiError extends Error {
	constructor(
		public status: number,
		public code: string,
		message: string
	) {
		super(message);
	}
}

let csrf = '';
export function setCsrf(token: string) {
	csrf = token;
}

/** Called when the server says the session is gone (401 on an authenticated call). */
let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
	onUnauthorized = fn;
}

async function toError(res: Response): Promise<ApiError> {
	try {
		const j = await res.json();
		return new ApiError(res.status, j?.error?.code ?? 'error', j?.error?.message ?? res.statusText);
	} catch {
		return new ApiError(res.status, 'error', res.statusText || `Request failed (${res.status})`);
	}
}

export async function api<T = unknown>(method: string, path: string, body?: unknown, signal?: AbortSignal): Promise<T> {
	const headers: Record<string, string> = { accept: 'application/json' };
	if (method !== 'GET') headers['x-csrf-token'] = csrf;
	let payload: BodyInit | undefined;
	if (body !== undefined) {
		headers['content-type'] = 'application/json';
		payload = JSON.stringify(body);
	}
	const res = await fetch('/api/v1' + path, { method, headers, body: payload, signal });
	if (res.status === 401 && !path.startsWith('/auth/login')) onUnauthorized();
	if (!res.ok) throw await toError(res);
	if (res.status === 204) return undefined as T;
	return (await res.json()) as T;
}

/** Sends raw bytes (file uploads); the body is streamed by the browser. */
export async function upload(path: string, data: Blob | ArrayBuffer | string, contentType = 'application/octet-stream'): Promise<Response> {
	const res = await fetch('/api/v1' + path, {
		method: path.includes('/extract') || path.endsWith('/upload') ? 'POST' : 'PUT',
		headers: { 'x-csrf-token': csrf, 'content-type': contentType },
		body: data
	});
	if (res.status === 401) onUnauthorized();
	if (!res.ok) throw await toError(res);
	return res;
}

export async function fetchText(path: string): Promise<string> {
	const res = await fetch('/api/v1' + path, { headers: { accept: '*/*' } });
	if (res.status === 401) onUnauthorized();
	if (!res.ok) throw await toError(res);
	return res.text();
}

export const MiB = 1024 * 1024;
export function fmtBytes(n: number): string {
	if (n >= 1024 ** 3) return (n / 1024 ** 3).toFixed(1) + ' GiB';
	if (n >= MiB) return Math.round(n / MiB) + ' MiB';
	if (n >= 1024) return Math.round(n / 1024) + ' KiB';
	return n + ' B';
}
export function fmtCpu(nano: number): string {
	return (nano / 1e9).toFixed(2).replace(/\.?0+$/, '') + ' CPU';
}

/** Reads a file as text together with its revision (ETag). */
export async function fetchRevision(path: string): Promise<{ text: string; etag: string | null }> {
	const res = await fetch('/api/v1' + path, { headers: { accept: '*/*' }, cache: 'no-store' });
	if (res.status === 401) onUnauthorized();
	if (!res.ok) throw await toError(res);
	return { text: await res.text(), etag: res.headers.get('etag') };
}

/**
 * Writes text with optimistic concurrency: ifMatch = the revision the editor
 * loaded (412 means someone else changed the file), createOnly refuses to
 * replace an existing file. Returns the new revision.
 */
export async function putText(path: string, text: string, opts: { ifMatch?: string | null; createOnly?: boolean } = {}): Promise<string | null> {
	const headers: Record<string, string> = { 'x-csrf-token': csrf, 'content-type': 'text/plain' };
	if (opts.ifMatch) headers['if-match'] = opts.ifMatch;
	if (opts.createOnly) headers['if-none-match'] = '*';
	const res = await fetch('/api/v1' + path, { method: 'PUT', headers, body: text });
	if (res.status === 401) onUnauthorized();
	if (!res.ok) throw await toError(res);
	return res.headers.get('etag');
}

/** Uploads a file with progress reporting; abort via the signal. */
export function uploadProgress(path: string, file: Blob, onProgress: (fraction: number) => void, signal?: AbortSignal, contentType = 'application/octet-stream'): Promise<Response> {
	return new Promise((resolve, reject) => {
		const xhr = new XMLHttpRequest();
		xhr.open(path.includes('/extract') ? 'POST' : 'PUT', '/api/v1' + path);
		xhr.setRequestHeader('x-csrf-token', csrf);
		xhr.setRequestHeader('content-type', contentType);
		xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded / e.total);
		xhr.onload = async () => {
			try {
				// A 204 (the file content route) must be built without a body:
				// `new Response('', { status: 204 })` throws, which used to leave
				// the upload pending forever and stall every following file.
				const empty = xhr.status === 204 || xhr.status === 205 || xhr.status === 304;
				const res = new Response(empty ? null : xhr.response, { status: xhr.status });
				if (xhr.status === 401) onUnauthorized();
				if (xhr.status >= 200 && xhr.status < 300) resolve(res);
				else reject(await toError(res));
			} catch {
				reject(new ApiError(xhr.status, 'invalid_response', 'The server answered the upload in an unexpected way.'));
			}
		};
		xhr.onerror = () => reject(new ApiError(0, 'network', 'The connection failed during the upload.'));
		xhr.onabort = () => reject(new ApiError(0, 'aborted', 'Upload cancelled.'));
		signal?.addEventListener('abort', () => xhr.abort());
		xhr.send(file);
	});
}
