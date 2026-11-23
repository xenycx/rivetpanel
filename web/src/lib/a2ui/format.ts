// Number formats shared by the A2UI TimeSeries and Table components.
import { fmtBytes } from '$lib/api/client';
import { fmtBps } from '$lib/api/admin';
import { fmtCores, fmtDuration } from '$lib/api/usage';

const num = (n: number) => n.toLocaleString(undefined, { maximumFractionDigits: Math.abs(n) >= 100 ? 0 : 2 });

/** A formatter for a chart unit or a table column format. */
export function formatter(unit: string): (n: number) => string {
	switch (unit) {
		case 'percent':
			return (n) => `${n >= 10 || n === 0 ? Math.round(n) : n.toFixed(1)}%`;
		case 'bytes':
			return (n) => fmtBytes(Math.round(n));
		case 'bytes_per_s':
			return fmtBps;
		case 'cores':
			return fmtCores;
		case 'duration_s':
			return (n) => fmtDuration(n * 1000);
		case 'time':
			return (n) => new Date(n).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit' });
		default:
			return num;
	}
}

/** A table cell as text: numbers through the column's format; ISO times read as times. */
export function cell(v: unknown, format: string | undefined): string {
	if (v == null || v === '') return '—';
	if (typeof v === 'boolean') return v ? 'yes' : 'no';
	if (format && format !== 'text') {
		let n = typeof v === 'number' ? v : typeof v === 'string' && v.trim() !== '' ? Number(v) : NaN;
		if (format === 'time' && typeof v === 'string' && !Number.isFinite(n)) n = Date.parse(v);
		if (Number.isFinite(n)) return formatter(format)(n);
	}
	return String(v);
}

/** Numeric columns line up on the right. */
export const NUMERIC = new Set(['number', 'percent', 'bytes', 'bytes_per_s', 'cores', 'duration_s']);
