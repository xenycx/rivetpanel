// Promise-based dialogs that replace window.confirm/prompt. One request is
// shown at a time; <DialogHost> (mounted once in the root layout) renders it.

export type Tone = 'primary' | 'danger';

export type Choice = { value: string; label: string; tone?: Tone | 'default' };

type Base = {
	title: string;
	/** Plain-language explanation of the effect. Newlines become paragraphs. */
	body?: string;
	/** Optional list of specifics shown under the body (names, dates, sizes). */
	details?: [string, string][];
};

export type ConfirmOptions = Base & {
	confirmLabel: string;
	cancelLabel?: string;
	tone?: Tone;
	/** Optional checkbox shown above the buttons (e.g. "Also restore variables"). */
	checkbox?: { label: string; hint?: string; checked?: boolean };
	/** Require typing this exact text before confirming (permanent deletion only). */
	typeToConfirm?: string;
};

export type PromptOptions = Base & {
	label: string;
	value?: string;
	placeholder?: string;
	confirmLabel: string;
	mono?: boolean;
	validate?: (v: string) => string | null;
	/** A live line under the field describing what the current value will do. */
	preview?: (v: string) => string | null;
	/** Select only the first n characters on open (a file name's stem). */
	selectTo?: number;
};

export type ChoiceOptions = Base & { choices: Choice[]; cancelLabel?: string };

type Req =
	| { kind: 'confirm'; o: ConfirmOptions; resolve: (v: { ok: boolean; checked: boolean }) => void }
	| { kind: 'prompt'; o: PromptOptions; resolve: (v: string | null) => void }
	| { kind: 'choice'; o: ChoiceOptions; resolve: (v: string | null) => void };

export const dialogs = $state<{ current: Req | null }>({ current: null });
const queue: Req[] = [];

function push(r: Req) {
	if (dialogs.current) queue.push(r);
	else dialogs.current = r;
}

/** Called by the host after the current request resolved. */
export function next() {
	dialogs.current = queue.shift() ?? null;
}

export function confirmDialog(o: ConfirmOptions): Promise<boolean> {
	return new Promise((resolve) => push({ kind: 'confirm', o, resolve: (v) => resolve(v.ok) }));
}

/** Like confirmDialog but also returns the checkbox state; null when cancelled. */
export function confirmWithOption(o: ConfirmOptions): Promise<{ checked: boolean } | null> {
	return new Promise((resolve) => push({ kind: 'confirm', o, resolve: (v) => resolve(v.ok ? { checked: v.checked } : null) }));
}

export function promptDialog(o: PromptOptions): Promise<string | null> {
	return new Promise((resolve) => push({ kind: 'prompt', o, resolve }));
}

/** Resolves with the chosen value, or null when cancelled. */
export function chooseDialog(o: ChoiceOptions): Promise<string | null> {
	return new Promise((resolve) => push({ kind: 'choice', o, resolve }));
}
