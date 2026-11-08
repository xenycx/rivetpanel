<script lang="ts">
	import { goto } from '$app/navigation';
	import { api, ApiError, fmtBytes } from '$lib/api/client';
	import { roleText, type Workspace } from '$lib/api/types';
	import { fmtAgo } from '$lib/args';
	import { toast } from '$lib/ui/toast.svelte';
	import { session } from '$lib/session.svelte';
	import { loadWorkspaces, selectWorkspace, workspaces, wsName } from '$lib/workspaces.svelte';
	import Icon from '$lib/components/ui/Icon.svelte';
	import Logo from '$lib/components/ui/Logo.svelte';
	import { logoSeed } from '$lib/avatar';
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
	// Who the workspace belongs to and your part in it, said once.
	function about(w: Workspace): string {
		const mine = w.owner_id === session.user?.id;
		if (w.personal) return mine ? 'Your personal workspace' : `Personal workspace of ${w.owner_email}`;
		if (mine) return 'Team · you own it';
		return `Team · ${w.role ? roleText[w.role] : 'administrator view'} · owned by ${w.owner_email}`;
	}
</script>

<svelte:head><title>Workspaces · RivetPanel</title></svelte:head>

<SettingsSection title="Your workspaces" description="A workspace groups apps{session.features.sites ? ' and sites' : ''} for a person or a team. Members see everything in it and act according to their role; your personal workspace is where things go by default.">
	{#if !workspaces.loaded}
		<Skeleton rows={2} />
	{:else}
		<ul class="grid gap-3 @container xl:grid-cols-2">
			{#each workspaces.list as w (w.id)}
				<li class="card flex flex-col p-4">
					<div class="flex items-start gap-3">
						<Logo src={w.logo_url} kind="workspace" seed={logoSeed(w.id, w.logo_variant)} class="size-10 rounded-tile" />
						<div class="min-w-0 flex-1">
							<a href="/settings/workspaces/{w.id}" class="block truncate font-semibold hover:underline" title={wsName(w)}>{wsName(w)}</a>
							<p class="truncate text-small text-muted" title={about(w)}>{about(w)}</p>
						</div>
						{#if workspaces.selected === w.id && workspaces.list.length > 1}<span class="pill" data-tone="action" title="The workspace the overview and Apps show">Selected</span>{/if}
					</div>
					<dl class="mt-4 grid grid-cols-2 gap-x-4 gap-y-2 border-t border-rule-soft pt-3 text-small whitespace-nowrap sm:grid-cols-4 xl:grid-cols-2">
						<div><dt class="eyebrow">Apps</dt><dd class="font-mono font-medium tabular-nums" title="{w.running_bots} running of {w.bots}">{w.running_bots}/{w.bots}</dd></div>
						<div><dt class="eyebrow">Sites</dt><dd class="font-mono font-medium tabular-nums">{w.sites}</dd></div>
						<div><dt class="eyebrow">Members</dt><dd class="font-mono font-medium tabular-nums">{w.members}</dd></div>
						<div><dt class="eyebrow">Memory</dt><dd class="font-mono font-medium tabular-nums">{fmtBytes(w.memory_bytes)}</dd></div>
					</dl>
					<p class="mt-2 text-small text-muted">{w.last_active_at_ms ? `Last active ${fmtAgo(w.last_active_at_ms)}` : 'No activity yet'}</p>
					<div class="mt-auto flex items-center gap-2 pt-4">
						<button class="btn btn-sm" onclick={() => open(w)}><Icon name="home" size={14} />Open overview</button>
						<a class="btn btn-sm btn-quiet" href="/settings/workspaces/{w.id}"><Icon name="gear" size={14} />Settings</a>
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
