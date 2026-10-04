<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { roleText, type Workspace } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import { session } from '$lib/session.svelte';
	import { loadWorkspaces, selectWorkspace, workspaces } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Notice from '$lib/components/ui/Notice.svelte';
	import Skeleton from '$lib/components/ui/Skeleton.svelte';
	import SettingsSection from '$lib/components/ui/SettingsSection.svelte';

	let name = $state('');
	let error = $state('');
	let busy = $state(false);
	$effect(() => {
		loadWorkspaces();
	});
	// Opened from the "New" menu: go straight to the name field.
	let nameField: HTMLInputElement | undefined = $state();
	$effect(() => {
		if (nameField && new URLSearchParams(location.search).get('new') === '1') {
			nameField.scrollIntoView({ block: 'center' });
			nameField.focus();
		}
	});

	async function create(e: SubmitEvent) {
		e.preventDefault();
		busy = true;
		error = '';
		try {
			const r = await api<{ workspace: Workspace }>('POST', '/workspaces', { name });
			name = '';
			await loadWorkspaces();
			toast(`Created ${r.workspace.name}`, 'success');
			goto(`/settings/workspaces/${r.workspace.id}`);
		} catch (err) {
			error = err instanceof ApiError ? err.message : 'The workspace could not be created.';
		} finally {
			busy = false;
		}
	}
	function open(w: Workspace) {
		selectWorkspace(w.id);
		goto('/dashboard');
	}
</script>

<svelte:head><title>Workspaces · RivetPanel</title></svelte:head>

<SettingsSection title="Your workspaces" description="A workspace groups bots{session.features.sites ? ' and sites' : ''} for a person or a team. Members see everything in it and act according to their role; your personal workspace is where things go by default.">
	{#if !workspaces.loaded}
		<Skeleton rows={2} />
	{:else}
		<ul class="grid gap-3 @container xl:grid-cols-2">
			{#each workspaces.list as w (w.id)}
				<li class="card flex flex-col p-4">
					<div class="flex items-start gap-3">
						<span class="grid size-10 shrink-0 place-items-center rounded-tile bg-action/12 font-mono text-small font-semibold text-action" aria-hidden="true">{(w.personal ? 'Personal' : w.name).slice(0, 2).toUpperCase()}</span>
						<div class="min-w-0 flex-1">
							<a href="/settings/workspaces/{w.id}" class="block truncate font-semibold hover:underline">{w.personal ? 'Personal' : w.name}</a>
							<p class="text-small text-muted">{w.role ? roleText[w.role] : 'Administrator view'}{w.personal ? '' : ` · owned by ${w.owner_id === session.user?.id ? 'you' : w.owner_email}`}</p>
						</div>
						{#if workspaces.selected === w.id}<span class="pill" data-tone="run">Selected</span>{/if}
					</div>
					<dl class="mt-4 grid grid-cols-4 gap-2 border-t border-rule-soft pt-3 text-small">
						<div><dt class="eyebrow">Bots</dt><dd class="font-mono font-medium">{w.running_bots}/{w.bots}</dd></div>
						<div><dt class="eyebrow">Sites</dt><dd class="font-mono font-medium">{w.sites}</dd></div>
						<div><dt class="eyebrow">Members</dt><dd class="font-mono font-medium">{w.members}</dd></div>
						<div><dt class="eyebrow">RAM</dt><dd class="font-mono font-medium">{fmtBytes(w.memory_bytes)}</dd></div>
					</dl>
					<div class="mt-4 flex flex-wrap items-center gap-2">
						<button class="btn btn-sm" onclick={() => open(w)}><Icon name="home" size={14} />Open overview</button>
						<a class="btn btn-sm btn-quiet" href="/settings/workspaces/{w.id}"><Icon name="users" size={14} />Members and settings</a>
						{#if w.last_active_at_ms}<span class="ml-auto text-small text-muted">Active {fmtAgo(w.last_active_at_ms)}</span>{/if}
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</SettingsSection>

<SettingsSection title="New team workspace" description="Create a workspace, then add people by the email address they sign in with. You become its owner.">
	<form class="card grid gap-3 p-5 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end" onsubmit={create}>
		<label class="block"><span class="label">Name</span><input class="field" required maxlength="64" bind:value={name} bind:this={nameField} placeholder="Community bots" /></label>
		<button class="btn btn-primary" disabled={busy || !name.trim()}><Icon name="plus" size={14} />Create workspace</button>
	</form>
	{#if error}<Notice tone="fail" class="mt-3" live>{error}</Notice>{/if}
</SettingsSection>
