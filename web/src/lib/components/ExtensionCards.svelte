<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { can, Perm, type Bot } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { session } from '$lib/session.svelte';
	import { extensions, loadExtensions } from '$lib/extensions/store.svelte';
	import type { BotExtension, ExtWidget } from '$lib/extensions/types';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Menu from '$lib/components/ui/Menu.svelte';

	// Cards that enabled extensions put on a bot's overview. Their content is
	// declarative widget JSON (docs/widgets.md), rendered here as text: no
	// extension HTML reaches the panel page. Extensions that read the bot's own
	// secrets (its Discord token) only work once switched on for this bot.
	let { bot }: { bot: Bot } = $props();

	type CardState = { ext: string; extName: string; id: string; title: string; path: string; widgets: ExtWidget[] | null; fetched?: number; error: string; busy: boolean };
	let switches = $state<BotExtension[]>([]);
	let cards = $state<CardState[]>([]);
	const canEnv = $derived(can(bot, Perm.env));

	onMount(async () => {
		if (!session.features.extensions) return;
		await loadExtensions();
		const withCards = extensions.list.filter((x) => x.bot_cards.length);
		if (!withCards.length) return;
		try {
			switches = (await api<{ extensions: BotExtension[] }>('GET', `/bots/${bot.id}/extensions`)).extensions;
		} catch {
			switches = [];
		}
		cards = withCards.flatMap((x) => x.bot_cards.map((c) => ({ ext: x.id, extName: x.name, id: c.id, title: c.title, path: c.path, widgets: null, error: '', busy: false })));
		for (const c of cards) if (isOn(c.ext)) load(c);
	});

	const sw = (ext: string) => switches.find((s) => s.id === ext);
	const isOn = (ext: string) => !sw(ext) || sw(ext)!.enabled;

	// fresh asks the extension to skip what it remembered (?refresh=1).
	async function load(c: CardState, fresh = false) {
		c.busy = true;
		try {
			const r = await api<{ widgets?: ExtWidget[]; fetched_at_ms?: number }>('GET', `/bots/${bot.id}/ext/${encodeURIComponent(c.ext)}${c.path}${fresh ? '?refresh=1' : ''}`);
			c.widgets = Array.isArray(r.widgets) ? r.widgets.slice(0, 24) : [];
			c.fetched = r.fetched_at_ms;
			c.error = '';
		} catch (e) {
			c.error = e instanceof ApiError ? e.message : 'The card could not be loaded.';
		} finally {
			c.busy = false;
		}
	}

	async function setOn(ext: string, on: boolean) {
		try {
			await api('PUT', `/bots/${bot.id}/extensions/${encodeURIComponent(ext)}`, { enabled: on });
			const s = sw(ext);
			if (s) s.enabled = on;
			for (const c of cards.filter((c) => c.ext === ext)) {
				c.widgets = null;
				if (on) load(c, true);
			}
			toast(on ? 'Turned on for this bot' : 'Turned off for this bot', 'success');
		} catch (e) {
			toast(e instanceof ApiError ? e.message : 'The change could not be saved.', 'fail');
		}
	}

	const text = (v: unknown) => (typeof v === 'string' ? v : '');
	const num = (v: unknown) => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
	function safeURL(v: unknown, image = false) {
		try {
			const u = new URL(text(v));
			if (u.protocol !== 'https:') return '';
			// Card images come from Discord's CDN only; links may go anywhere over https.
			return !image || u.hostname === 'cdn.discordapp.com' ? u.href : '';
		} catch {
			return '';
		}
	}
	const tone = (v: unknown) => ({ good: 'bg-run', warn: 'bg-warn', bad: 'bg-fail' })[text(v)] ?? 'bg-muted';
	const items = (v: unknown) => (Array.isArray(v) ? v.slice(0, 20) : []) as { key?: unknown; value?: unknown }[];
</script>

{#each cards as c (c.ext + c.id)}
	<section aria-labelledby="ext-card-{c.id}">
		<div class="flex items-center gap-2">
			<h3 id="ext-card-{c.id}" class="min-w-0 flex-1 truncate text-title font-semibold">{c.title}</h3>
			{#if isOn(c.ext) && c.widgets}
				<button class="btn btn-sm btn-quiet btn-icon" disabled={c.busy} onclick={() => load(c, true)} aria-label="Refresh {c.title}" title="Refresh"><Icon name="restart" size={13} /></button>
			{/if}
			{#if sw(c.ext)?.enabled && canEnv}
				<Menu label="{c.title} options" items={[{ label: 'Turn off for this bot', onselect: () => setOn(c.ext, false) }]} />
			{/if}
		</div>
		{#if !isOn(c.ext)}
			<p class="mt-2 text-small text-muted">{sw(c.ext)?.explanation}</p>
			{#if canEnv}
				<button class="btn btn-sm mt-2" onclick={() => setOn(c.ext, true)}>Turn on for this bot</button>
			{:else}
				<p class="mt-1 text-small text-muted">Someone who can change this bot's variables can turn it on.</p>
			{/if}
		{:else if c.error}
			<p class="mt-2 text-small text-fail">{c.error}</p>
		{:else if !c.widgets}
			<p class="mt-2 text-muted">Loading…</p>
		{:else}
			<div class="mt-2 space-y-2.5">
				{#each c.widgets as w (w.key)}
					{#if w.kind === 'image' && safeURL(w.data.url, true)}
						<div class="flex items-center gap-3">
							<img src={safeURL(w.data.url, true)} alt={text(w.data.alt)} class="size-10 rounded-pill object-cover" referrerpolicy="no-referrer" loading="lazy" />
							<span class="font-medium">{w.title}</span>
						</div>
					{:else if w.kind === 'status'}
						<p class="flex items-center gap-2"><span class="size-2 shrink-0 rounded-pill {tone(w.data.state)}" aria-hidden="true"></span><span class="text-muted">{w.title}:</span> <span class="font-medium">{text(w.data.text)}</span></p>
					{:else if w.kind === 'metric'}
						<p>
							<span class="text-2xl font-semibold tabular-nums">{num(w.data.value).toLocaleString()}</span> <span class="text-small text-muted">{text(w.data.unit)}</span>
							{#if text(w.data.detail)}<span class="block text-small text-muted">{text(w.data.detail)}</span>{/if}
						</p>
					{:else if w.kind === 'kv'}
						<dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-small">
							{#each items(w.data.items) as it, i (i)}<dt class="text-muted">{text(it.key)}</dt><dd class="truncate" title={text(it.value)}>{text(it.value)}</dd>{/each}
						</dl>
					{:else if w.kind === 'link' && safeURL(w.data.url)}
						<a class="btn btn-sm" href={safeURL(w.data.url)} target="_blank" rel="noopener noreferrer"><Icon name="external" size={13} />{text(w.data.label) || w.title}</a>
					{:else if w.kind === 'text' || w.kind === 'markdown'}
						<p class="text-small whitespace-pre-wrap text-ink/90">{text(w.data.text)}</p>
					{/if}
				{/each}
				<p class="text-small text-muted">{c.extName}{#if c.fetched}, checked {fmtAgo(c.fetched)}{/if}</p>
			</div>
		{/if}
	</section>
{/each}
