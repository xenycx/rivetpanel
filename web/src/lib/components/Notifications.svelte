<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { can, Perm, type Bot } from '$lib/api/types';
	import { session } from '$lib/session.svelte';
	import { toast } from '$lib/ui/toast.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	// Which events tell the owner about this bot or game server. The messages go
	// to the owner's account (the panel's notification inbox, email and the
	// Discord channel they connected), never through a Discord bot.
	let { bot }: { bot: Bot } = $props();

	type Prefs = { crash: boolean; deploy: boolean; backup: boolean; recovery: boolean; heartbeat_after_s: number };
	let saved = $state<Prefs | null>(null);
	let prefs = $state<Prefs | null>(null);
	let webhook = $state(false);
	let error = $state('');
	let saving = $state(false);
	const isGame = $derived(bot.kind === 'game');
	const noun = $derived(isGame ? 'server' : 'bot');
	const admin = $derived(can(bot, Perm.admin));
	const path = $derived(`/bots/${bot.id}`);
	const msg = (e: unknown) => (e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) : 'The request failed.');

	onMount(async () => {
		try {
			const h = await api<{ alerts: Prefs; webhook: boolean }>('GET', `${path}/health`);
			saved = { ...h.alerts };
			prefs = { ...h.alerts };
			webhook = h.webhook;
		} catch (e) {
			error = msg(e);
		}
	});

	type Kind = { key: 'crash' | 'deploy' | 'backup' | 'recovery'; label: string; hint: string };
	const kinds = $derived<Kind[]>(
		isGame
			? [
					{ key: 'crash', label: 'Crashes', hint: 'When the server crashes, or its installation or start fails (at most one message per 10 minutes)' },
					{ key: 'backup', label: 'Backup failures', hint: 'When a backup of this server fails' }
				]
			: [
					{ key: 'crash', label: 'Crashes', hint: 'When the bot crashes, or its build or start fails (at most one message per 10 minutes)' },
					{ key: 'deploy', label: 'Deployments', hint: 'When a GitHub deployment succeeds or fails' },
					{ key: 'backup', label: 'Backup failures', hint: 'When a backup of this bot fails' },
					{ key: 'recovery', label: 'Recoveries', hint: 'When heartbeats resume after a "stopped reporting" message' }
				]
	);
	const thresholds = [0, 60, 120, 300, 600, 1800, 3600];
	const thrLabel = (s: number) => (s === 0 ? 'Off' : s < 3600 ? `${s / 60} minute${s === 60 ? '' : 's'}` : '1 hour');
	const dirty = $derived(!!prefs && !!saved && JSON.stringify(prefs) !== JSON.stringify(saved));

	async function save() {
		if (!prefs) return;
		saving = true;
		try {
			const p = await api<Prefs>('PUT', `${path}/alerts`, prefs);
			saved = { ...p };
			prefs = { ...p };
			toast('Notification choices saved', 'success');
		} catch (e) {
			toast(msg(e), 'fail');
		} finally {
			saving = false;
		}
	}
	async function test() {
		try {
			await api('POST', `${path}/alerts/test`);
			toast('Test message sent', 'success');
		} catch (e) {
			toast(msg(e), 'fail');
		}
	}
</script>

<SettingsSection
	id="notifications"
	title="Notifications"
	description="Tell the {noun}'s owner when something needs attention. Messages go to the owner's account: the panel's notification bell, email when it is on in their notification settings, and the Discord channel they connected (Settings → Connected accounts). {isGame ? 'Nothing is sent to players or to the game.' : 'They are not sent by your Discord bot.'} Saved at once; the {noun} does not need to stop."
>
	{#snippet aside()}
		{#if admin && webhook}<button class="btn btn-sm mt-3" onclick={test}><Icon name="bolt" size={14} />Send a test to Discord</button>{/if}
		<p class="mt-2 text-small"><a class="link" href="/settings/notifications">Delivery settings for your account</a></p>
		{#if !webhook && session.features.oauth}<p class="mt-1 text-small"><a class="link" href="/settings/connected-accounts">Connect Discord</a></p>{/if}
	{/snippet}
	{#if error}
		<Notice tone="fail">{error}</Notice>
	{:else if !prefs}
		<p class="text-muted">Loading…</p>
	{:else}
		<p class="mb-3 text-small text-muted">
			Discord: {webhook ? "connected, messages also go to the owner's Discord channel." : 'not connected by the owner. Messages still reach the panel inbox and email.'}
		</p>
		<fieldset class="grid gap-2 sm:grid-cols-2" disabled={!admin}>
			<legend class="sr-only">Send a message for</legend>
			{#each kinds as k (k.key)}
				<label class="flex items-start gap-2.5 rounded-tile border border-rule-soft bg-panel px-3 py-2.5 transition-colors has-[:checked]:border-action/40">
					<input type="checkbox" class="mt-0.5" bind:checked={prefs[k.key]} />
					<span>{k.label}<span class="help mt-0">{k.hint}</span></span>
				</label>
			{/each}
		</fieldset>
		{#if !isGame}
			<label class="mt-4 block max-w-sm">
				<span class="label">Tell me when the bot stops reporting for</span>
				<select class="field" bind:value={prefs.heartbeat_after_s} disabled={!admin}>
					{#each thresholds as s (s)}<option value={s}>{thrLabel(s)}</option>{/each}
				</select>
				<span class="help">Needs the SDK heartbeat (see <a class="link" href="/bots/{bot.id}?tab=alerts">Health</a>). One message when it goes quiet, and one when it comes back (with Recoveries on).</span>
			</label>
		{:else}
			<p class="mt-3 text-small text-muted">Server health is the process state and the game query (players, latency and version in the header). A server that keeps running but stops answering the query does not send a message.</p>
		{/if}
		{#if admin}
			<div class="mt-4 flex gap-2">
				<button class="btn btn-primary" disabled={!dirty || saving} onclick={save}>Save notifications</button>
				{#if dirty}<button class="btn btn-quiet" onclick={() => saved && (prefs = { ...saved })}>Discard</button>{/if}
			</div>
		{:else}
			<p class="mt-3 text-small text-muted">Only someone with full control of this {noun} can change these.</p>
		{/if}
	{/if}
</SettingsSection>
