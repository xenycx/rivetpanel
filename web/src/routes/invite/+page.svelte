<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { api, ApiError } from '$lib/api/client';
	import { Perm } from '$lib/api/types';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';

	// The token is in the URL fragment: browsers never send it to a server
	// or put it in a Referer header.
	type Invite = { bot_id: string; bot_name: string; permissions: number; created_by: string; expires_at_ms: number };
	let token = $state('');
	let invite = $state<Invite | null>(null);
	let error = $state('');
	let busy = $state(false);
	const labels: [number, string][] = [
		[Perm.console, 'See the console, analytics and resource use'],
		[Perm.power, 'Start, stop and restart it'],
		[Perm.files, 'Edit files, packages, deployments and backups'],
		[Perm.env, 'Read and change environment variables'],
		[Perm.admin, 'Change every setting (full admin)']
	];

	onMount(async () => {
		token = decodeURIComponent(location.hash.slice(1));
		history.replaceState(null, '', '/invite'); // do not keep the token in history
		if (!token) {
			error = 'This link has no invitation in it. Ask for a new link.';
			return;
		}
		try {
			invite = await api<Invite>('POST', '/invites/preview', { token });
		} catch (e) {
			error = e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) + '.' : 'The panel could not be reached.';
		}
	});

	async function accept() {
		busy = true;
		try {
			const r = await api<Invite>('POST', '/invites/accept', { token });
			await goto(`/bots/${r.bot_id}`);
		} catch (e) {
			error = e instanceof ApiError ? e.message.charAt(0).toUpperCase() + e.message.slice(1) + '.' : 'The panel could not be reached.';
		} finally {
			busy = false;
		}
	}
</script>

<svelte:head><title>Invitation · RivetPanel</title></svelte:head>

<section class="hero mx-auto max-w-lg py-8">
	<p class="eyebrow">Invitation</p>
	{#if error}
		<h1 class="mt-1 text-page">This invitation does not work<span class="text-action">.</span></h1>
		<Notice tone="fail" class="mt-4">{error}</Notice>
		<a class="btn mt-4" href="/dashboard">Back to your bots</a>
	{:else if !invite}
		<p class="mt-2 text-muted">Checking the invitation…</p>
	{:else}
		<h1 class="mt-1 text-page">Join {invite.bot_name}<span class="text-action">.</span></h1>
		<p class="mt-1 text-muted">{invite.created_by} invited you. The link expires {new Date(invite.expires_at_ms).toLocaleString()}.</p>
		<ul class="mt-5 grid gap-2 rounded-overlay border border-rule-soft bg-panel p-4">
			{#each labels.filter(([bit]) => invite && (invite.permissions & bit || invite.permissions & Perm.admin)) as [bit, text] (bit)}
				<li class="flex items-start gap-2"><Icon name="check" class="mt-[3px] text-run" />{text}</li>
			{/each}
		</ul>
		<div class="mt-5 flex gap-2">
			<button class="btn btn-primary" disabled={busy} onclick={accept}>Accept invitation</button>
			<a class="btn" href="/dashboard">Not now</a>
		</div>
	{/if}
</section>
