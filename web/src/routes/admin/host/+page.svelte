<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { page } from '$app/state';
	import { api } from '$lib/api/client';
	import type { HostSnapshot } from '$lib/api/admin';
	import { poll } from '$lib/poll';
	import HostOverview from '$lib/components/HostOverview.svelte';
	import HostBots from '$lib/components/HostBots.svelte';
	import HostCapacity from '$lib/components/HostCapacity.svelte';
	import PanelLogs from '$lib/components/PanelLogs.svelte';
	import Icon, { type IconName } from '$lib/components/ui/Icon.svelte';

	const tabs: { id: string; label: string; icon: IconName }[] = [
		{ id: 'resources', label: 'Resources', icon: 'chart' },
		{ id: 'bots', label: 'Bots', icon: 'box' },
		{ id: 'capacity', label: 'Capacity', icon: 'sliders' },
		{ id: 'logs', label: 'Panel logs', icon: 'terminal' }
	];
	const asked = $derived(page.url.searchParams.get('tab') ?? 'resources');
	const tab = $derived(tabs.some((t) => t.id === asked) ? asked : 'resources');
	const go = (id: string) => goto(id === 'resources' ? '/admin/host' : `/admin/host?tab=${id}`, { keepFocus: true, noScroll: true });

	// One snapshot for the page: the overview shows it, the Logs tab flags errors.
	let snap = $state<HostSnapshot | null>(null);
	onMount(() =>
		poll(async () => {
			try {
				snap = await api<HostSnapshot>('GET', '/admin/host');
			} catch {
				/* each tab reports its own failure */
			}
		}, 10_000)
	);
	const errors = $derived(snap && snap.logs.errors > 0 && Date.now() - snap.logs.last_error_ms < 3600_000 ? snap.logs.errors : 0);
</script>

<svelte:head><title>Host · RivetPanel</title></svelte:head>
<h2 class="text-section">Host</h2>
<p class="mt-1 max-w-3xl text-muted">The machine that runs the bots, RivetPanel itself and what each bot uses. Samples are taken every 30 seconds and kept for the time set under <a class="link" href="/admin/logs">Logs and retention</a> (7 days by default, or <code>RIVET_TELEMETRY_RETENTION</code>).</p>

<nav class="mt-4 flex gap-0.5 overflow-x-auto border-b border-rule-soft [scrollbar-width:none] [&::-webkit-scrollbar]:hidden" aria-label="Host sections">
	{#each tabs as t (t.id)}
		<a
			href={t.id === 'resources' ? '/admin/host' : `/admin/host?tab=${t.id}`}
			class="relative flex shrink-0 items-center gap-2 px-3 py-2.5 text-small font-medium whitespace-nowrap {tab === t.id ? 'text-ink after:absolute after:inset-x-2 after:-bottom-px after:h-0.5 after:bg-action' : 'text-muted hover:text-ink'}"
			aria-current={tab === t.id ? 'page' : undefined}
			onclick={(e) => { e.preventDefault(); void go(t.id); }}
		>
			<Icon name={t.icon} />{t.label}
			{#if t.id === 'logs' && errors}<span class="pill" data-tone="fail" title="Errors logged since the panel started">{errors}</span>{/if}
		</a>
	{/each}
</nav>

<div class="mt-5">
	{#if tab === 'resources'}<HostOverview {snap} goto={go} />
	{:else if tab === 'bots'}<HostBots />
	{:else if tab === 'capacity'}<HostCapacity />
	{:else}<PanelLogs />{/if}
</div>
