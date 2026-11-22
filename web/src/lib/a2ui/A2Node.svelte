<script lang="ts" module>
	import type { IconName } from '$lib/components/ui/Icon.svelte';
	// A2UI's icon names drawn with the panel's own 16px stroke set.
	const ICONS: Record<string, IconName> = {
		accountCircle: 'users', person: 'users', add: 'plus', arrowBack: 'chevronLeft', arrowForward: 'chevronRight', skipPrevious: 'chevronLeft', skipNext: 'chevronRight', rewind: 'chevronLeft', fastForward: 'chevronRight',
		attachFile: 'paperclip', calendarToday: 'clock', event: 'clock', camera: 'image', photo: 'image', check: 'check', close: 'x', delete: 'trash', download: 'download', upload: 'upload', edit: 'pencil',
		error: 'alert', warning: 'alert', help: 'info', info: 'info', favorite: 'star', favoriteOff: 'star', star: 'star', starHalf: 'star', starOff: 'star', folder: 'folder', home: 'home', locationOn: 'globe',
		lock: 'lock', lockOpen: 'lock', mail: 'send', send: 'send', menu: 'menu', moreVert: 'more', moreHoriz: 'more', notifications: 'bell', notificationsOff: 'bell', pause: 'pause', play: 'play', stop: 'stop',
		refresh: 'restart', search: 'search', settings: 'gear', share: 'external', shoppingCart: 'package', payment: 'key', print: 'file', visibility: 'eye', visibilityOff: 'eyeOff', call: 'bell', phone: 'bell',
		volumeUp: 'bell', volumeDown: 'bell', volumeMute: 'bell', volumeOff: 'bell'
	};
	const JUSTIFY: Record<string, string> = { start: 'justify-start', center: 'justify-center', end: 'justify-end', spaceBetween: 'justify-between', spaceAround: 'justify-around', spaceEvenly: 'justify-evenly', stretch: 'a2-stretch' };
	const ALIGN: Record<string, string> = { start: 'items-start', center: 'items-center', end: 'items-end', stretch: 'items-stretch' };
	const TONES: Record<string, string> = { good: 'run', warn: 'warn', bad: 'fail', info: 'info', neutral: '' };
	const IMAGE: Record<string, string> = { icon: 'size-6', avatar: 'size-10 rounded-pill', smallFeature: 'size-16 rounded-tile', mediumFeature: 'max-h-48 w-full rounded-tile', largeFeature: 'max-h-80 w-full rounded-tile', header: 'h-28 w-full rounded-tile' };
	const FIT: Record<string, string> = { contain: 'contain', cover: 'cover', fill: 'fill', none: 'none', scaleDown: 'scale-down' };
	/** Components that travel on their own when a surface updates. */
	const MORPH = new Set(['Card', 'Metric', 'Badge', 'Button', 'Image', 'Code']);
	const CUSTOM = new Set(['Heading', 'Metric', 'Badge', 'Code', 'Link']);
	const escapeKey = (k: string) => k.replace(/~/g, '~0').replace(/\//g, '~1');
	let radios = 0;
</script>

<script lang="ts">
	import { getContext } from 'svelte';
	import { Tween } from 'svelte/motion';
	import { cubicOut } from 'svelte/easing';
	import Self from './A2Node.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import SafeMarkdown from '$lib/components/SafeMarkdown.svelte';
	import { HOST, type Host } from './A2Surface.svelte';
	import { PANEL_CATALOG, absolute, evaluate, failing, getAt, safeHref, samePath, setAt, truthy, type Scope } from './model.svelte';

	// One component of an A2UI surface, and (recursively) its children.
	let {
		id,
		scope,
		seq = 0,
		depth = 0,
		inline = false,
		inCard = false,
		trigger
	}: { id: string; scope: Scope; seq?: number; depth?: number; inline?: boolean; inCard?: boolean; trigger?: () => void } = $props();

	const host = getContext<Host>(HOST);
	const s = $derived(host.surface);
	const c = $derived(s.components[id]);
	const allowed = $derived(!!c && (!CUSTOM.has(c.component) || s.catalogId === PANEL_CATALOG));
	const ev = (v: unknown) => evaluate(v, s, scope);
	const str = (v: unknown): string => {
		const x = ev(v);
		return x == null ? '' : typeof x === 'object' ? JSON.stringify(x) : String(x);
	};

	// Inputs write to the path they are bound to; an unbound literal keeps the
	// person's edit locally.
	let local = $state<Record<string, unknown>>({});
	const read = (prop: string) => (prop in local ? local[prop] : ev(c?.[prop]));
	function write(prop: string, value: unknown) {
		const b = c?.[prop];
		if (b && typeof b === 'object' && typeof b['@path'] === 'string') {
			const root = setAt(s.data, absolute(b['@path'], scope), value);
			if (root !== s.data) s.data = root;
		} else local[prop] = value;
	}
	let touched = $state(false);
	const fails = $derived(c && Array.isArray(c['@checks']) ? failing(c, s, scope) : []);
	const showError = $derived(touched && fails.length > 0);

	const key = $derived(`${id}@${scope.path}`);
	const a11y = $derived((c?.['@accessibility'] ?? {}) as Record<string, unknown>);
	const common = $derived({
		style: `--a2-i:${Math.min(seq, 14)};${typeof c?.weight === 'number' ? `flex:${c.weight} 1 9rem;min-width:0;` : ''}`,
		'aria-label': a11y.label != null ? str(a11y.label) : undefined,
		'aria-live': (a11y.live === 'polite' || a11y.live === 'assertive' ? a11y.live : undefined) as 'polite' | 'assertive' | undefined,
		'aria-hidden': a11y.hidden != null && truthy(ev(a11y.hidden)) ? ('true' as const) : undefined,
		'data-a2-morph': c && MORPH.has(c.component) ? key : undefined
	});

	function entries(items: unknown): string[] {
		if (Array.isArray(items)) return items.slice(0, 200).map((_, i) => String(i));
		if (items && typeof items === 'object') return Object.keys(items).slice(0, 200);
		return [];
	}

	// Buttons: an event goes to the assistant with its context resolved now;
	// openUrl opens a panel page or an https address.
	function press() {
		if (trigger) return trigger();
		const a = c?.action;
		if (a?.event && typeof a.event.name === 'string') {
			const context: Record<string, unknown> = {};
			for (const [k, v] of Object.entries((a.event.context ?? {}) as Record<string, unknown>)) context[k] = $state.snapshot(ev(v));
			host.act(
				{ name: a.event.name, surfaceId: s.id, sourceComponentId: id, timestamp: new Date().toISOString(), context, ...(a.event.userMessage != null ? { userMessage: str(a.event.userMessage) } : {}) },
				str(s.components[c.child]?.text)
			);
		} else if (a?.functionCall?.['@call'] === 'openUrl') {
			const url = str(a.functionCall.args?.url);
			if (safeHref(url)) host.open(url);
		}
	}

	// Metrics glide between readings.
	const reading = $derived.by(() => {
		if (c?.component !== 'Metric') return null;
		const v = ev(c.value);
		const n = typeof v === 'number' ? v : typeof v === 'string' && v.trim() !== '' ? Number(v) : NaN;
		return Number.isFinite(n) ? n : null;
	});
	const decimals = $derived(reading == null ? 0 : Math.min(2, (String(reading).split('.')[1] ?? '').length));
	const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
	const tween = new Tween(0, { duration: reduce ? 0 : 700, easing: cubicOut });
	let first = true;
	$effect(() => {
		const n = reading;
		if (n == null) return;
		void tween.set(n, first ? { duration: 0 } : undefined);
		first = false;
	});
	const fmt = (n: number) => n.toLocaleString(undefined, { minimumFractionDigits: decimals, maximumFractionDigits: decimals });

	let tab = $state(0);
	let modal = $state(false);
	let filter = $state('');
	const radioName = `a2-choice-${++radios}`;
	function pick(value: string, multi: boolean) {
		const cur = read('value');
		const list = Array.isArray(cur) ? cur.map(String) : cur ? [String(cur)] : [];
		write('value', multi ? (list.includes(value) ? list.filter((x) => x !== value) : [...list, value]) : [value]);
		touched = true;
	}
	const sliderStep = (min: number, max: number) => (typeof c?.steps === 'number' && c.steps >= 1 ? (max - min) / c.steps : Number.isInteger(min) && Number.isInteger(max) ? 1 : 'any');
	const dateType = $derived(c?.enableDate && c?.enableTime ? 'datetime-local' : c?.enableTime ? 'time' : 'date');
	function dateValue(v: unknown): string {
		const t = v == null ? '' : String(v);
		if (dateType === 'datetime-local') return t.slice(0, 16);
		if (dateType === 'time') return /T(\d\d:\d\d)/.exec(t)?.[1] ?? t.slice(0, 5);
		return t.slice(0, 10);
	}
</script>

{#snippet kids(children: unknown, card: boolean)}
	{#if Array.isArray(children)}
		{#each children as cid, i}
			<Self id={String(cid)} {scope} seq={seq + i + 1} depth={depth + 1} inCard={card} />
		{/each}
	{:else if children && typeof children === 'object' && typeof (children as any).componentId === 'string'}
		{@const t = children as { path: string; componentId: string }}
		{@const base = absolute(t.path, scope)}
		{#each entries(getAt(s.data, base)) as k, i (k)}
			<Self id={t.componentId} scope={{ path: `${base === '/' ? '' : base}/${escapeKey(k)}`, index: i }} seq={seq + Math.min(i, 10) + 1} depth={depth + 1} inCard={card} />
		{/each}
	{/if}
{/snippet}

{#if depth > 40}
	<!-- Deeper than any real interface: stop. -->
{:else if !c}
	<span class="a2-missing" aria-hidden="true"></span>
{:else if !allowed}
	<p class="a2-in text-small text-muted" {...common}>This part ({c.component}) cannot be shown here.</p>
{:else if c.component === 'Text'}
	{#if inline}<span {...common}>{str(c.text)}</span>
	{:else if c.variant === 'caption'}<p class="a2-in text-small text-muted" {...common}>{str(c.text)}</p>
	{:else}<div class="a2-in a2-text min-w-0" {...common}><SafeMarkdown source={str(c.text)} relativeLinks /></div>{/if}
{:else if c.component === 'Heading'}
	<h3 class="a2-in text-[1.0625rem] leading-snug font-semibold text-balance text-ink" {...common}>{str(c.text)}</h3>
{:else if c.component === 'Image'}
	{@const url = str(c.url)}
	{#if samePath(url)}
		<img class="a2-in block bg-paper-2 {IMAGE[c.variant] ?? IMAGE.mediumFeature}" style:object-fit={FIT[c.fit] ?? 'cover'} src={url} alt={str(c.description)} loading="lazy" referrerpolicy="no-referrer" {...common} />
	{/if}
{:else if c.component === 'Icon'}
	{#if c.name && typeof c.name === 'object' && 'svgPath' in c.name}
		<svg class="a2-in shrink-0 text-muted" width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" style={common.style}><path d={str(c.name.svgPath)} /></svg>
	{:else}
		<span class="a2-in inline-flex text-muted" {...common}><Icon name={ICONS[str(c.name)] ?? 'info'} size={16} /></span>
	{/if}
{:else if c.component === 'Row'}
	<div class="a2-in flex min-w-0 flex-wrap gap-x-2.5 gap-y-2 {JUSTIFY[c.justify] ?? ''} {ALIGN[c.align] ?? 'items-center'}" {...common}>{@render kids(c.children, inCard)}</div>
{:else if c.component === 'Column'}
	<div class="a2-in flex min-w-0 flex-col gap-2.5 {JUSTIFY[c.justify] ?? ''} {ALIGN[c.align] ?? 'items-stretch'}" {...common}>{@render kids(c.children, inCard)}</div>
{:else if c.component === 'List'}
	{#if c.direction === 'horizontal'}
		<div class="a2-in a2-hlist flex snap-x gap-2 overflow-x-auto pb-1 {ALIGN[c.align] ?? 'items-stretch'}" {...common}>{@render kids(c.children, inCard)}</div>
	{:else}
		<div class="a2-in a2-vlist flex min-w-0 flex-col {ALIGN[c.align] ?? 'items-stretch'}" role="list" {...common}>{@render kids(c.children, inCard)}</div>
	{/if}
{:else if c.component === 'Card'}
	{#if inCard}
		<div class="a2-in flex min-w-0 flex-col gap-2.5" {...common}><Self id={c.child} {scope} seq={seq + 1} depth={depth + 1} inCard /></div>
	{:else}
		<div class="a2-in a2-card min-w-0" {...common}><Self id={c.child} {scope} seq={seq + 1} depth={depth + 1} inCard /></div>
	{/if}
{:else if c.component === 'Tabs'}
	{@const tabs = Array.isArray(c.tabs) ? c.tabs : []}
	<div class="a2-in min-w-0" {...common}>
		<div class="seg" role="tablist">
			{#each tabs as t, i}
				<button type="button" role="tab" class="seg-item" aria-selected={tab === i} onclick={() => (tab = i)}>{str(t.title)}</button>
			{/each}
		</div>
		<div role="tabpanel" class="mt-2.5">
			{#if tabs[tab]}<Self id={tabs[tab].child} {scope} seq={seq + 1} depth={depth + 1} {inCard} />{/if}
		</div>
	</div>
{:else if c.component === 'Modal'}
	{#if s.components[c.trigger]?.component === 'Button'}
		<Self id={c.trigger} {scope} {seq} depth={depth + 1} {inCard} trigger={() => (modal = true)} />
	{:else}
		<button type="button" class="a2-in block w-full rounded-tile text-left" onclick={() => (modal = true)} {...common}><Self id={c.trigger} {scope} {seq} depth={depth + 1} {inCard} /></button>
	{/if}
	<Dialog bind:open={modal} title={a11y.label != null ? str(a11y.label) : 'Details'}>
		<div class="a2-surface flex flex-col gap-2.5"><Self id={c.content} {scope} depth={depth + 1} inCard /></div>
	</Dialog>
{:else if c.component === 'Divider'}
	{#if c.axis === 'vertical'}<div class="a2-in w-px self-stretch bg-rule-soft" role="separator" aria-orientation="vertical" {...common}></div>
	{:else}<hr class="a2-in my-0.5 border-0 border-t border-rule-soft" {...common} />{/if}
{:else if c.component === 'Button'}
	{@const event = !trigger && !!c.action?.event}
	{@const external = !trigger && !!c.action?.functionCall && !samePath(str(c.action.functionCall.args?.url))}
	{@const waiting = event && host.locked()}
	<button
		type="button"
		class="a2-in btn btn-sm {c.variant === 'primary' ? 'btn-primary' : c.variant === 'borderless' ? 'btn-quiet' : ''}"
		disabled={fails.length > 0 || waiting}
		title={fails[0] ?? (waiting ? 'Wait for the assistant to finish' : undefined)}
		onclick={press}
		{...common}
	>
		{#if c.child}<Self id={c.child} {scope} depth={depth + 1} inline />{/if}
		{#if external}<Icon name="external" size={12} class="opacity-70" />{/if}
	</button>
{:else if c.component === 'TextField'}
	{@const variant = c.variant ?? 'shortText'}
	{@const value = read('value')}
	<label class="a2-in block min-w-0" {...common}>
		<span class="label text-small">{str(c.label)}</span>
		{#if variant === 'longText'}
			<textarea class="field min-h-[4.5rem] resize-y" rows="3" value={value == null ? '' : String(value)} placeholder={str(c.placeholder) || undefined} aria-invalid={showError} oninput={(e) => write('value', e.currentTarget.value)} onblur={() => (touched = true)}></textarea>
		{:else}
			<input class="field" type="text" inputmode={variant === 'number' ? 'decimal' : undefined} value={value == null ? '' : String(value)} placeholder={str(c.placeholder) || undefined} aria-invalid={showError} oninput={(e) => write('value', e.currentTarget.value)} onblur={() => (touched = true)} />
		{/if}
		{#if showError}<span class="mt-1 block text-small text-fail">{fails[0]}</span>{/if}
	</label>
{:else if c.component === 'CheckBox'}
	<label class="a2-in flex cursor-pointer items-start gap-2.5 text-small" {...common}>
		<input type="checkbox" class="mt-0.5 size-4 shrink-0" checked={truthy(read('value'))} onchange={(e) => { write('value', e.currentTarget.checked); touched = true; }} />
		<span>{str(c.label)}{#if showError}<span class="block text-fail">{fails[0]}</span>{/if}</span>
	</label>
{:else if c.component === 'ChoicePicker'}
	{@const multi = c.variant === 'multipleSelection'}
	{@const cur = read('value')}
	{@const chosen = Array.isArray(cur) ? cur.map(String) : cur ? [String(cur)] : []}
	{@const options = (Array.isArray(c.options) ? c.options : []) as { label: unknown; value: string }[]}
	{@const shown = filter ? options.filter((o) => str(o.label).toLowerCase().includes(filter.toLowerCase())) : options}
	<fieldset class="a2-in min-w-0" {...common}>
		{#if c.label}<legend class="label text-small">{str(c.label)}</legend>{/if}
		{#if c.filterable && options.length > 6}<input class="field mb-1.5" type="search" placeholder="Filter" aria-label="Filter the choices" bind:value={filter} />{/if}
		{#if c.displayStyle === 'chips'}
			<div class="flex flex-wrap gap-1.5" role={multi ? 'group' : 'radiogroup'}>
				{#each shown as o (o.value)}
					{@const on = chosen.includes(o.value)}
					<button type="button" class="a2-chip" role={multi ? 'checkbox' : 'radio'} aria-checked={on} onclick={() => pick(o.value, multi)}>
						{#if on}<Icon name="check" size={12} />{/if}{str(o.label)}
					</button>
				{/each}
			</div>
		{:else}
			<div class="grid gap-0.5">
				{#each shown as o (o.value)}
					<label class="flex cursor-pointer items-center gap-2.5 rounded-control px-1.5 py-1 text-small hover:bg-paper-2/60">
						<input type={multi ? 'checkbox' : 'radio'} name={radioName} class="size-4 shrink-0" checked={chosen.includes(o.value)} onchange={() => pick(o.value, multi)} />{str(o.label)}
					</label>
				{/each}
			</div>
		{/if}
		{#if showError}<span class="mt-1 block text-small text-fail">{fails[0]}</span>{/if}
	</fieldset>
{:else if c.component === 'Slider'}
	{@const min = Number(c.min ?? 0)}
	{@const max = Number(c.max)}
	{@const value = Number(read('value') ?? min)}
	<label class="a2-in block min-w-0" {...common}>
		<span class="mb-1 flex items-baseline justify-between gap-3 text-small"><span class="font-medium">{str(c.label)}</span><output class="font-mono text-[.78rem] text-muted tabular-nums">{Number.isFinite(value) ? value.toLocaleString(undefined, { maximumFractionDigits: 2 }) : ''}</output></span>
		<input type="range" class="a2-range w-full" {min} {max} step={sliderStep(min, max)} value={Number.isFinite(value) ? value : min} oninput={(e) => write('value', Number(e.currentTarget.value))} />
	</label>
{:else if c.component === 'DateTimeInput'}
	<label class="a2-in block min-w-0" {...common}>
		{#if c.label}<span class="label text-small">{str(c.label)}</span>{/if}
		<input class="field" type={dateType} value={dateValue(read('value'))} min={c.min != null ? dateValue(ev(c.min)) : undefined} max={c.max != null ? dateValue(ev(c.max)) : undefined} oninput={(e) => write('value', e.currentTarget.value)} />
	</label>
{:else if c.component === 'Metric'}
	{@const tone = TONES[str(c.tone)] ?? ''}
	{@const max = c.max != null ? Number(ev(c.max)) : NaN}
	{@const pct = reading != null && Number.isFinite(max) && max > 0 ? Math.max(0, Math.min(100, (tween.current / max) * 100)) : null}
	<div class="a2-in a2-metric self-stretch {inCard ? '' : 'stat'}" data-tone={tone || undefined} {...common}>
		<span class="block truncate text-small text-muted">{str(c.label)}</span>
		<span class="a2-value mt-0.5 block text-title leading-tight font-semibold tabular-nums">{reading != null ? fmt(tween.current) : str(c.value)}{#if c.unit}<span class="ml-0.5 text-small font-normal text-muted">{str(c.unit)}</span>{/if}</span>
		{#if pct != null}<div class="meter mt-1.5" data-level={tone === 'fail' || (!tone && pct >= 90) ? 'high' : tone === 'warn' || (!tone && pct >= 75) ? 'warn' : undefined} role="meter" aria-valuemin={0} aria-valuemax={max} aria-valuenow={reading} aria-label={str(c.label)}><i style:width="{pct}%"></i></div>{/if}
		{#if c.detail}<span class="mt-1 block text-[.72rem] text-muted">{str(c.detail)}</span>{/if}
	</div>
{:else if c.component === 'Badge'}
	<span class="a2-in pill self-center" data-tone={TONES[str(c.tone)] || undefined} {...common}>{str(c.text)}</span>
{:else if c.component === 'Code'}
	<pre class="a2-in max-h-56 min-w-0 overflow-auto rounded-control bg-term p-2.5 font-mono text-[.72rem] leading-snug whitespace-pre-wrap text-term-ink" {...common}><code data-language={typeof c.language === 'string' ? c.language : undefined}>{str(c.text)}</code></pre>
{:else if c.component === 'Link'}
	{@const href = str(c.href)}
	{#if samePath(href)}<a class="a2-in link" {href} {...common}>{str(c.text)}</a>
	{:else if safeHref(href)}<a class="a2-in link inline-flex items-center gap-1" {href} target="_blank" rel="noopener noreferrer" referrerpolicy="no-referrer" {...common}>{str(c.text)}<Icon name="external" size={11} /></a>
	{:else}<span class="a2-in" {...common}>{str(c.text)}</span>{/if}
{:else}
	<p class="a2-in text-small text-muted" {...common}>This part ({c.component}) cannot be shown here.</p>
{/if}
