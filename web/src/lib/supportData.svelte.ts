import { api } from '$lib/api/client';
import type { Label, SavedReply, StaffMember, DeskSettings } from '$lib/api/support';

// Support data shared by the ticket views: labels, the staff who can be
// assigned or mentioned, and saved replies. Loaded once per page session and
// refreshed after changes; every list is optional (empty for requesters).

export const support = $state<{ labels: Label[]; staff: StaffMember[]; replies: SavedReply[]; desk: DeskSettings }>({
	labels: [],
	staff: [],
	replies: [],
	desk: { auto_reply: false, response_hours: 0, response_time: '', max_files: 10, max_file_bytes: 10 << 20 }
});

let loaded = { labels: false, staff: false, replies: false, desk: false };

/** The desk settings: usual response time, upload limits (and, for managers, the automatic reply). */
export async function loadDesk(force = false) {
	if (loaded.desk && !force) return support.desk;
	loaded.desk = true;
	try {
		support.desk = await api<DeskSettings>('GET', '/tickets/settings');
	} catch {
		loaded.desk = false; // keep the defaults; try again next time
	}
	return support.desk;
}

export async function loadLabels(force = false) {
	if (loaded.labels && !force) return support.labels;
	loaded.labels = true;
	try {
		support.labels = (await api<{ labels: Label[] }>('GET', '/tickets/labels')).labels;
	} catch {
		support.labels = [];
	}
	return support.labels;
}

export async function loadStaff(force = false) {
	if (loaded.staff && !force) return support.staff;
	loaded.staff = true;
	try {
		support.staff = (await api<{ staff: StaffMember[] }>('GET', '/tickets/staff')).staff;
	} catch {
		support.staff = [];
	}
	return support.staff;
}

export async function loadReplies(force = false) {
	if (loaded.replies && !force) return support.replies;
	loaded.replies = true;
	try {
		support.replies = (await api<{ replies: SavedReply[] }>('GET', '/tickets/replies')).replies;
	} catch {
		support.replies = [];
	}
	return support.replies;
}

export const labelById = (id: string) => support.labels.find((l) => l.id === id);
