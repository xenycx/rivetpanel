<script lang="ts">
	import { api, ApiError } from '$lib/api/client';
	import type { Bot, Site, SitesInfo } from '$lib/api/types';
	import Dialog from '$lib/components/ui/Dialog.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { toast } from '$lib/ui/toast.svelte';

	// Gives a container app a web address: a site that forwards to the port
	// it listens on, on a sites domain or on one of the user's own domains.
	let { open = $bindable(false), bot, onDone }: { open?: boolean; bot: Bot; onDone?: () => void } = $props();

	let info = $state<SitesInfo | null>(null);
	let slug = $state('');
	let address = $state('');
	let port = $state(8080);
	let health = $state('/');
	let busy = $state(false);
	let error = $state('');

	const bases = $derived(info?.domains ?? []);
	const mine = $derived(info?.my_domains ?? []);
	const own = $derived(address.startsWith('mine:') ? mine.find((m) => `mine:${m.id}` === address) : undefined);
	const label = $derived(slug || 'your-app');
	const preview = $derived((own?.example_url ?? bases.find((b) => b.id === address)?.example_url ?? info?.example_url ?? '').replace('://example.', `://${label}.`));
	const suggest = (n: string) => n.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 30);

	let wasOpen = false;
	$effect(() => {
		if (open && !wasOpen) {
			error = '';
			slug = suggest(bot.name);
			port = bot.ports[0]?.container_port ?? 8080;
			health = '/';
			api<SitesInfo>('GET', '/sites-info')
				.then((i) => {
					info = i;
					const def = i.my_domains?.find((m) => m.default);
					address = def ? `mine:${def.id}` : (i.domains?.find((b) => b.primary)?.id ?? '');
				})
				.catch((e) => (error = e instanceof ApiError ? e.message : 'Site hosting settings could not be loaded.'));
		}
		wasOpen = open;
	});

	async function submit(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			await api<Site>('POST', `/bots/${bot.id}/web-addresses`, {
				slug,
				port,
				health_path: health,
				domain_id: own ? (bases.find((b) => b.primary)?.id ?? '') : address,
				user_domain_id: own?.id ?? ''
			});
			toast(`${bot.name} now answers at ${preview.replace(/^https?:\/\//, '')}`);
			open = false;
			onDone?.();
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The web address could not be added. Check your connection and try again.';
		} finally {
			busy = false;
		}
	}
</script>

<Dialog bind:open title="Add a web address" size="md">
	{#if info && !info.enabled}
		<Notice>Site hosting is turned off on this panel, so apps cannot get a web address. An administrator turns it on with <code>RIVET_SITES_LISTEN</code>.</Notice>
	{:else}
		<form id="web-address-form" class="grid gap-4" onsubmit={submit}>
			<label class="block">
				<span class="label">Address</span>
				<div class="flex items-stretch">
					<input class="field min-w-0 font-mono {bases.length > 1 || mine.length ? 'rounded-r-none' : ''}" required minlength="3" maxlength="40" bind:value={slug} oninput={(e) => (slug = e.currentTarget.value.toLowerCase())} aria-describedby="wa-address" />
					{#if bases.length > 1 || mine.length}
						<select class="field w-auto max-w-[55%] rounded-l-none border-l-0 font-mono" bind:value={address} aria-label="Domain">
							{#if mine.length}
								<optgroup label="Your domains">{#each mine as m (m.id)}<option value="mine:{m.id}">.{m.domain}</option>{/each}</optgroup>
								<optgroup label="Sites domains">{#each bases as b (b.id)}<option value={b.id}>.{b.domain}</option>{/each}</optgroup>
							{:else}
								{#each bases as b (b.id)}<option value={b.id}>.{b.domain}</option>{/each}
							{/if}
						</select>
					{:else if info}
						<span class="flex items-center truncate rounded-r-control border border-l-0 border-rule px-3 font-mono text-small text-muted">.{bases[0]?.domain ?? info.domain}</span>
					{/if}
				</div>
				<span class="help" id="wa-address">
					Answers at <code class="text-ink">{preview}</code>{#if own}, verified by your record on {own.domain}. Point <code class="text-ink">{label}.{own.domain}</code> at this server, or use a wildcard record{/if}.
					{#if !mine.length}Prove your own domain under <a class="link" href="/settings/domains">Settings → Domains</a> to use it here.{/if}
				</span>
			</label>
			<div class="grid gap-4 sm:grid-cols-2">
				<label class="block">
					<span class="label">Container port</span>
					<input class="field font-mono" type="number" required min="1" max="65535" bind:value={port} aria-describedby="wa-port" />
					<span class="help" id="wa-port">The port the app listens on inside the container{bot.ports.length ? `; it publishes ${bot.ports.map((p) => p.container_port).join(', ')}` : ''}.</span>
				</label>
				<label class="block">
					<span class="label">Health check path</span>
					<input class="field font-mono" required bind:value={health} placeholder="/" />
					<span class="help">Must answer with a success code.</span>
				</label>
			</div>
			<p class="text-small text-muted">The container restarts once to join the sites network. Its published ports keep working.</p>
			{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
		</form>
	{/if}
	{#snippet footer()}
		<button class="btn" onclick={() => (open = false)}>Cancel</button>
		<button class="btn btn-primary" type="submit" form="web-address-form" disabled={busy || !info?.enabled || slug.length < 3}>{busy ? 'Adding…' : 'Add web address'}</button>
	{/snippet}
</Dialog>
