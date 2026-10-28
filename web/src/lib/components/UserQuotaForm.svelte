<script lang="ts">
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import type { QuotaUsage, UserQuotaView } from '$lib/api/types';
	import Notice from '$lib/components/ui/Notice.svelte';
	import { session } from '$lib/session.svelte';

	let { userId }: { userId: string } = $props();

	type Field = { key: 'max_apps' | 'max_bots' | 'max_games' | 'max_web_apps' | 'max_sites'; label: string; used: keyof QuotaUsage; fallback: 'max_apps' | 'max_sites' | null };
	const fields: Field[] = [
		{ key: 'max_bots', label: 'Bots', used: 'bots', fallback: null },
		{ key: 'max_games', label: 'Game servers', used: 'games', fallback: null },
		{ key: 'max_web_apps', label: 'Web apps', used: 'web_apps', fallback: null },
		{ key: 'max_apps', label: 'Apps in total', used: 'apps', fallback: 'max_apps' },
		{ key: 'max_sites', label: 'Sites', used: 'sites', fallback: 'max_sites' }
	];

	let v = $state<UserQuotaView | null>(null);
	// Number inputs bind numbers, or null when emptied.
	let form = $state<Record<string, number | string | null>>({});
	let error = $state('');
	let saved = $state('');
	let busy = $state(false);
	// Only built-in administrators change quotas; account managers read them.
	const editable = $derived(session.user?.role === 'admin');

	function fill(r: UserQuotaView) {
		v = r;
		const q = r.quota;
		form = Object.fromEntries(fields.map((f) => [f.key, q[f.key] == null ? '' : String(q[f.key])]));
		form.memory_mib = q.memory_bytes == null ? '' : String(Math.round(q.memory_bytes / 2 ** 20));
	}
	$effect(() => {
		if (!userId) return;
		api<UserQuotaView>('GET', `/admin/users/${userId}/quota`)
			.then(fill)
			.catch((e) => (error = e instanceof ApiError ? e.message : 'The quota could not be loaded.'));
	});

	function placeholder(f: Field): string {
		const d = f.fallback && v ? v.defaults[f.fallback] : 0;
		return d ? `Panel default: ${d}` : 'Panel default: unlimited';
	}
	const memoryPlaceholder = $derived(v?.defaults.memory_bytes ? `Panel default: ${fmtBytes(v.defaults.memory_bytes)}` : 'Panel default: unlimited');
	const num = (s: number | string | null | undefined) => (s == null || String(s).trim() === '' ? null : Number(s));

	async function save(body: Record<string, number | null>) {
		busy = true;
		error = saved = '';
		try {
			fill(await api<UserQuotaView>('PUT', `/admin/users/${userId}/quota`, body));
			saved = 'Quota saved. Apps and sites the account already has are not touched.';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The quota could not be saved.';
		} finally {
			busy = false;
		}
	}
	function submit(e: SubmitEvent) {
		e.preventDefault();
		const body: Record<string, number | null> = Object.fromEntries(fields.map((f) => [f.key, num(form[f.key])]));
		const mib = num(form.memory_mib);
		body.memory_bytes = mib == null ? null : Math.round(mib * 2 ** 20);
		if (Object.values(body).some((n) => n != null && (!Number.isInteger(n) || n < 0))) {
			error = 'Limits must be whole numbers of 0 or more; leave a field empty for the panel default.';
			return;
		}
		save(body);
	}
	async function reset() {
		busy = true;
		error = saved = '';
		try {
			await api('DELETE', `/admin/users/${userId}/quota`);
			fill(await api<UserQuotaView>('GET', `/admin/users/${userId}/quota`));
			saved = 'The panel defaults apply to this account again.';
		} catch (e) {
			error = e instanceof ApiError ? e.message : 'The quota could not be cleared.';
		} finally {
			busy = false;
		}
	}
</script>

<section class="mt-8" aria-labelledby="quota-h">
	<h3 id="quota-h" class="mb-1 text-title font-semibold">Limits</h3>
	<p class="mb-3 text-small text-muted">
		Leave a field empty to use the panel default; 0 allows none. Stopped apps count until they are deleted. Lowering a limit never stops or removes what the account already has; it only blocks new apps, sites and memory.
	</p>
	{#if error}<Notice tone="fail" live>{error}</Notice>{/if}
	{#if saved}<Notice tone="success" live>{saved}</Notice>{/if}
	{#if v}
		{#if v.exempt}<p class="mb-3 text-small text-muted">This account is an administrator: per-account limits do not apply to it.</p>{/if}
		<form class="card grid gap-4 p-5 sm:grid-cols-2 lg:grid-cols-3" onsubmit={submit}>
			{#each fields as f (f.key)}
				<label class="block">
					<span class="label">{f.label} <span class="font-normal text-muted">· {v.usage[f.used]} in use</span></span>
					<input class="field font-mono" type="number" min="0" step="1" placeholder={placeholder(f)} bind:value={form[f.key]} disabled={!editable || busy} />
				</label>
			{/each}
			<label class="block">
				<span class="label">Memory (MiB) <span class="font-normal text-muted">· {fmtBytes(v.usage.memory_bytes)} assigned</span></span>
				<input class="field font-mono" type="number" min="0" step="1" placeholder={memoryPlaceholder} bind:value={form.memory_mib} disabled={!editable || busy} />
			</label>
			{#if editable}
				<div class="flex flex-wrap items-center gap-2 sm:col-span-2 lg:col-span-3">
					<button class="btn btn-primary" type="submit" disabled={busy}>Save limits</button>
					<button class="btn" type="button" disabled={busy} onclick={() => save({ max_bots: 1, max_games: 0, max_web_apps: 0, max_sites: 0, max_apps: null, memory_bytes: null })}>One bot only</button>
					{#if v.updated_at_ms}<button class="btn" type="button" disabled={busy} onclick={reset}>Use panel defaults</button>{/if}
				</div>
			{/if}
		</form>
	{/if}
</section>
