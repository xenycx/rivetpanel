/** Splits a command line into arguments the way a shell would quote them, but
 * without any expansion: the panel runs the result directly, never via a shell. */
export function splitArgs(s: string): string[] {
	const out: string[] = [];
	let cur = '';
	let has = false;
	let quote: '"' | "'" | null = null;
	for (let i = 0; i < s.length; i++) {
		const c = s[i];
		if (quote) {
			if (c === quote) quote = null;
			else if (c === '\\' && quote === '"' && i + 1 < s.length && (s[i + 1] === '"' || s[i + 1] === '\\')) cur += s[++i];
			else cur += c;
		} else if (c === '"' || c === "'") {
			quote = c;
			has = true;
		} else if (/\s/.test(c)) {
			if (has || cur) out.push(cur);
			cur = '';
			has = false;
		} else {
			cur += c;
			has = true;
		}
	}
	if (has || cur) out.push(cur);
	return out;
}

/** Inverse of splitArgs for display. */
export function joinArgs(a: string[]): string {
	return a.map((x) => (x === '' || /[\s"'\\]/.test(x) ? `"${x.replace(/(["\\])/g, '\\$1')}"` : x)).join(' ');
}

export function fmtDuration(ms: number): string {
	if (ms >= 3600_000) return `${+(ms / 3600_000).toFixed(1)} h`;
	if (ms >= 60_000) return `${+(ms / 60_000).toFixed(1)} min`;
	return `${+(ms / 1000).toFixed(1)} s`;
}

export function fmtTime(ms: number): string {
	return new Date(ms).toLocaleString();
}

/** "just now", "5 min ago", "3 h ago", "2 days ago", then a date. */
export function fmtAgo(ms: number, now = Date.now()): string {
	const s = Math.round((now - ms) / 1000);
	if (s < 45) return 'just now';
	if (s < 3600) return `${Math.round(s / 60)} min ago`;
	if (s < 86400) return `${Math.round(s / 3600)} h ago`;
	if (s < 86400 * 14) return `${Math.round(s / 86400)} day${Math.round(s / 86400) === 1 ? '' : 's'} ago`;
	return new Date(ms).toLocaleDateString();
}

/** The future counterpart of fmtAgo: "in 5 min", "in 3 h", "in 2 days", then a date; "expired" once past. */
export function fmtUntil(ms: number, now = Date.now()): string {
	const s = Math.round((ms - now) / 1000);
	if (s <= 0) return 'expired';
	if (s < 60) return 'in under a minute';
	if (s < 3600) return `in ${Math.round(s / 60)} min`;
	if (s < 86400) return `in ${Math.round(s / 3600)} h`;
	const d = Math.round(s / 86400);
	if (d < 45) return `in ${d} day${d === 1 ? '' : 's'}`;
	return new Date(ms).toLocaleDateString();
}

/** Short date and time without seconds. */
export function fmtWhen(ms: number): string {
	return new Date(ms).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}
