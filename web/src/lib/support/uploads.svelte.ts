import { getCsrf } from '$lib/api/client';
import { support } from '$lib/supportData.svelte';

// Files a message will carry. Each file uploads as soon as it is added
// (with progress), so sending is instant; the server keeps it private to
// the uploader until a message lists its id. Pictures preview from the
// local file, never from the server.

export type UploadItem = {
	key: string;
	name: string;
	size: number;
	type: string;
	/** A local object URL for pictures. */
	preview: string | null;
	/** 0–1 while uploading. */
	progress: number;
	state: 'uploading' | 'done' | 'error';
	error: string;
	/** The server's id once stored. */
	id: string | null;
	xhr: XMLHttpRequest | null;
};

const imageTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
let seq = 0;

export class Uploads {
	items = $state<UploadItem[]>([]);

	/** Ids of the stored files, in the order they were added. */
	get ids() {
		return this.items.filter((i) => i.state === 'done' && i.id).map((i) => i.id!);
	}
	get busy() {
		return this.items.some((i) => i.state === 'uploading');
	}
	get failed() {
		return this.items.some((i) => i.state === 'error');
	}
	get count() {
		return this.items.length;
	}

	add(files: Iterable<File>) {
		const max = support.desk.max_files;
		for (const f of files) {
			const item: UploadItem = {
				key: `u${++seq}`,
				name: f.name || (f.type.startsWith('image/') ? `pasted-${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.${f.type.split('/')[1] ?? 'png'}` : 'file'),
				size: f.size,
				type: f.type,
				preview: imageTypes.includes(f.type) ? URL.createObjectURL(f) : null,
				progress: 0,
				state: 'uploading',
				error: '',
				id: null,
				xhr: null
			};
			if (this.items.length >= max) {
				item.state = 'error';
				item.error = `At most ${max} files per message.`;
			} else if (f.size > support.desk.max_file_bytes) {
				item.state = 'error';
				item.error = `Larger than ${Math.round(support.desk.max_file_bytes / 1024 / 1024)} MB.`;
			} else if (f.size === 0) {
				item.state = 'error';
				item.error = 'The file is empty.';
			}
			this.items.push(item);
			if (item.state === 'uploading') this.send(item.key, f);
		}
	}

	private patch(key: string, p: Partial<UploadItem>) {
		const i = this.items.findIndex((x) => x.key === key);
		if (i >= 0) this.items[i] = { ...this.items[i], ...p };
	}

	private send(key: string, f: File) {
		const xhr = new XMLHttpRequest();
		this.patch(key, { xhr });
		xhr.open('POST', `/api/v1/tickets/attachments?name=${encodeURIComponent(this.items.find((x) => x.key === key)?.name ?? f.name)}`);
		xhr.setRequestHeader('x-csrf-token', getCsrf());
		xhr.setRequestHeader('content-type', 'application/octet-stream');
		xhr.upload.onprogress = (e) => e.lengthComputable && this.patch(key, { progress: e.loaded / e.total });
		xhr.onload = () => {
			let body: { id?: string; error?: { message?: string } } = {};
			try {
				body = JSON.parse(xhr.responseText);
			} catch {
				/* not JSON */
			}
			if (xhr.status === 201 && body.id) this.patch(key, { state: 'done', id: body.id, progress: 1, xhr: null });
			else this.patch(key, { state: 'error', error: body.error?.message ?? `Upload failed (${xhr.status}).`, xhr: null });
		};
		xhr.onerror = () => this.patch(key, { state: 'error', error: 'The connection dropped. Try again.', xhr: null });
		xhr.send(f);
	}

	/** Remove a file from the message (and from the server if stored). */
	remove(key: string) {
		const it = this.items.find((x) => x.key === key);
		if (!it) return;
		it.xhr?.abort();
		if (it.preview) URL.revokeObjectURL(it.preview);
		if (it.id) fetch(`/api/v1/tickets/attachments/${it.id}`, { method: 'DELETE', headers: { 'x-csrf-token': getCsrf() } }).catch(() => {});
		this.items = this.items.filter((x) => x.key !== key);
	}

	/** Forget every file after the message was sent (the server keeps them). */
	sent() {
		for (const it of this.items) if (it.preview) URL.revokeObjectURL(it.preview);
		this.items = [];
	}

	/** Remove every file (cancel). */
	discard() {
		for (const it of [...this.items]) this.remove(it.key);
	}
}

/** Files from a drop or paste event (none for plain text). */
export function filesOf(dt: DataTransfer | null): File[] {
	if (!dt) return [];
	return [...dt.files];
}

/** True while a drag carries files (not text or a link from the page). */
export const dragHasFiles = (e: DragEvent) => !!e.dataTransfer && [...e.dataTransfer.types].includes('Files');

/** The files of each ticket's unsent reply, kept while the panel stays open (like drafts). */
const byTicket = new Map<string, Uploads>();
export function uploadsFor(key: string): Uploads {
	let u = byTicket.get(key);
	if (!u) byTicket.set(key, (u = new Uploads()));
	return u;
}
