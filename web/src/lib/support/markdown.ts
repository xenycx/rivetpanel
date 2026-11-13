// Markdown formatting for a plain textarea: each action rewrites the value
// around the selection and returns the new value with the new selection,
// so the caller keeps the textarea's undo-free state in its own binding.

export type Edit = { value: string; start: number; end: number };
export type FormatAction = 'bold' | 'italic' | 'code' | 'link' | 'quote' | 'bullets' | 'numbers' | 'codeBlock';

/** Wrap the selection in before/after, or unwrap it when it already is. */
function wrap(v: string, s: number, e: number, before: string, after: string, placeholder: string): Edit {
	const sel = v.slice(s, e);
	if (v.slice(s - before.length, s) === before && v.slice(e, e + after.length) === after) {
		return { value: v.slice(0, s - before.length) + sel + v.slice(e + after.length), start: s - before.length, end: e - before.length };
	}
	if (sel.startsWith(before) && sel.endsWith(after) && sel.length >= before.length + after.length) {
		const inner = sel.slice(before.length, sel.length - after.length);
		return { value: v.slice(0, s) + inner + v.slice(e), start: s, end: s + inner.length };
	}
	const text = sel || placeholder;
	return { value: v.slice(0, s) + before + text + after + v.slice(e), start: s + before.length, end: s + before.length + text.length };
}

/** The whole lines the selection touches. */
function lines(v: string, s: number, e: number) {
	const from = v.lastIndexOf('\n', s - 1) + 1;
	let to = v.indexOf('\n', e > s && v[e - 1] === '\n' ? e - 1 : e);
	if (to < 0) to = v.length;
	return { from, to, text: v.slice(from, to) };
}

/** Prefix every selected line (toggle off when all already carry it). */
function prefix(v: string, s: number, e: number, mark: (i: number) => string, test: RegExp): Edit {
	const { from, to, text } = lines(v, s, e);
	const ls = text.split('\n');
	const on = ls.every((l) => test.test(l) || !l.trim());
	const out = on ? ls.map((l) => l.replace(test, '')) : ls.map((l, i) => (l.trim() || ls.length === 1 ? mark(i) + l.replace(test, '') : l));
	const joined = out.join('\n');
	return { value: v.slice(0, from) + joined + v.slice(to), start: from, end: from + joined.length };
}

export function format(action: FormatAction, v: string, s: number, e: number): Edit {
	switch (action) {
		case 'bold':
			return wrap(v, s, e, '**', '**', 'bold text');
		case 'italic':
			return wrap(v, s, e, '_', '_', 'italic text');
		case 'code':
			return v.slice(s, e).includes('\n') ? format('codeBlock', v, s, e) : wrap(v, s, e, '`', '`', 'code');
		case 'link': {
			const sel = v.slice(s, e);
			if (/^https?:\/\/\S+$/.test(sel)) {
				const out = `[link text](${sel})`;
				return { value: v.slice(0, s) + out + v.slice(e), start: s + 1, end: s + 10 };
			}
			const text = sel || 'link text';
			const out = `[${text}](https://)`;
			const urlAt = s + text.length + 3;
			return { value: v.slice(0, s) + out + v.slice(e), start: urlAt, end: urlAt + 8 };
		}
		case 'quote':
			return prefix(v, s, e, () => '> ', /^>\s?/);
		case 'bullets':
			return prefix(v, s, e, () => '- ', /^\s*[-*]\s+/);
		case 'numbers':
			return prefix(v, s, e, (i) => `${i + 1}. `, /^\s*\d+\.\s+/);
		case 'codeBlock': {
			const sel = v.slice(s, e) || 'code';
			const lead = s > 0 && v[s - 1] !== '\n' ? '\n' : '';
			const tail = e < v.length && v[e] !== '\n' ? '\n' : '';
			const out = `${lead}\`\`\`\n${sel}\n\`\`\`${tail}`;
			const at = s + lead.length + 4;
			return { value: v.slice(0, s) + out + v.slice(e), start: at, end: at + sel.length };
		}
	}
}

/** The formatting shortcut a key event asks for (Ctrl/⌘ + B, I, E, K, Shift+7/8). */
export function shortcut(e: KeyboardEvent): FormatAction | null {
	if (!(e.ctrlKey || e.metaKey) || e.altKey) return null;
	const k = e.key.toLowerCase();
	if (!e.shiftKey) return k === 'b' ? 'bold' : k === 'i' ? 'italic' : k === 'e' ? 'code' : k === 'k' ? 'link' : null;
	return e.code === 'Digit7' ? 'numbers' : e.code === 'Digit8' ? 'bullets' : e.code === 'Period' ? 'quote' : null;
}

/** Pressing Enter inside a list continues it; on an empty item it ends the list. */
export function continueList(v: string, s: number, e: number): Edit | null {
	if (s !== e) return null;
	const from = v.lastIndexOf('\n', s - 1) + 1;
	const line = v.slice(from, s);
	const m = /^(\s*)(?:([-*])|(\d+)\.)\s+(.*)$/.exec(line);
	if (!m) return null;
	if (!m[4].trim()) {
		// An empty item: drop the marker and leave the list.
		return { value: v.slice(0, from) + v.slice(s), start: from, end: from };
	}
	const next = m[2] ? `${m[1]}${m[2]} ` : `${m[1]}${Number(m[3]) + 1}. `;
	const ins = '\n' + next;
	return { value: v.slice(0, s) + ins + v.slice(e), start: s + ins.length, end: s + ins.length };
}

/** Bytes as "820 KB", "2.4 MB". */
export function fmtBytes(n: number): string {
	if (n < 1024) return `${n} B`;
	if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
	return `${(n / 1024 / 1024).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`;
}
