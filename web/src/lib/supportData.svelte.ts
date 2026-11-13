import { api } from '$lib/api/client';
import type { Label, SavedReply, StaffMember } from '$lib/api/support';

// Support data shared by the ticket views: labels, the staff who can be
// assigned or mentioned, and saved replies. Loaded once per page session and
// refreshed after changes; every list is optional (empty for requesters).

export const support = $state<{ labels: Label[]; staff: StaffMember[]; replies: SavedReply[] }>({ labels: [], staff: [], replies: [] });

let loaded = { labels: false, staff: false, replies: false };

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
