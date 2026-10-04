<script lang="ts">
	import { onMount } from 'svelte';
	import { api, ApiError } from '$lib/api/client';
	import { toast } from '$lib/ui/toast.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	type Category = { name: string; label: string; description: string; email: boolean; alert: boolean; in_panel: boolean; email_on: boolean };
	let categories = $state<Category[]>([]);
	let emailAvailable = $state(false);
	let error = $state('');
	let saving = $state('');
	const msg = (e: unknown) => (e instanceof ApiError ? e.message : 'The request failed.');

	onMount(async () => {
		try {
			const r = await api<{ categories: Category[]; email_available: boolean }>('GET', '/me/notification-prefs');
			categories = r.categories;
			emailAvailable = r.email_available;
		} catch (e) {
			error = msg(e);
		}
	});

	async function save(c: Category) {
		saving = c.name;
		error = '';
		try {
			const r = await api<{ categories: Category[] }>('PUT', '/me/notification-prefs', { prefs: [{ category: c.name, in_panel: c.in_panel, email: c.email_on }] });
			categories = r.categories;
			toast(`Saved: ${c.label}`);
		} catch (e) {
			error = msg(e);
		} finally {
			saving = '';
		}
	}
</script>

<svelte:head><title>Notifications · RivetPanel</title></svelte:head>

<SettingsSection title="Notifications" description="Choose, for each kind of event, whether it appears under the bell in the top bar and whether it is emailed to you.">
	{#if !emailAvailable}
		<Notice class="mb-4">This panel does not send email yet, so only in-panel notifications are delivered.</Notice>
	{/if}
	<div class="card overflow-x-auto">
		<table class="w-full text-left">
			<thead class="text-small text-muted">
				<tr class="border-b border-rule-soft">
					<th class="px-4 py-2.5 font-medium">Event</th>
					<th class="w-24 px-4 py-2.5 text-center font-medium">In panel</th>
					<th class="w-24 px-4 py-2.5 text-center font-medium">Email</th>
				</tr>
			</thead>
			<tbody class="divide-y divide-rule-soft">
				{#each categories as c (c.name)}
					<tr>
						<td class="px-4 py-3">
							<div class="font-medium">{c.label}</div>
							<div class="text-small text-muted">{c.description}</div>
						</td>
						<td class="px-4 py-3 text-center">
							<input type="checkbox" class="size-4 accent-[var(--color-action)]" aria-label="{c.label}: in panel" bind:checked={c.in_panel} disabled={saving === c.name} onchange={() => save(c)} />
						</td>
						<td class="px-4 py-3 text-center">
							{#if c.email}
								<input type="checkbox" class="size-4 accent-[var(--color-action)]" aria-label="{c.label}: email" bind:checked={c.email_on} disabled={saving === c.name} onchange={() => save(c)} />
							{:else}
								<span class="text-small text-muted" title="Announcement email follows the notice and news setting on your profile">profile</span>
							{/if}
						</td>
					</tr>
				{/each}
			</tbody>
		</table>
	</div>
	<p class="mt-3 text-small text-muted">
		Email goes only to a verified address. Alerts, deployments, backups and node notices are emailed only while <a class="link" href="/settings/profile">Alert emails</a> is on; Discord alerts follow each bot's alert settings. Security notices about your account are always emailed. Notifications are kept for 90 days (at most 200).
	</p>
	{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}
</SettingsSection>
